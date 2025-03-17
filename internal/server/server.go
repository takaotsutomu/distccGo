package server

import (
	"context"
	"fmt"
	"log"
	"net"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	pb "github.com/takaotsutomu/distccGo/internal/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// BuildServer is the gRPC server implementing the BuildService
type BuildServer struct {
	pb.UnimplementedBuildServiceServer

	// Job management
	pendingJobs   chan *pb.JobRequest // Channel of pending jobs (both compile and link)
	jobStatus     map[string]jobStatusInfo
	jobStatusLock sync.RWMutex

	// Job details storage
	compileJobs    map[string]*pb.CompileJobRequest
	linkJobs       map[string]*pb.LinkJobRequest
	jobDetailsLock sync.RWMutex

	// Worker management
	availableWorkers chan string           // Channel of available worker IDs
	workers          map[string]workerInfo // Map of worker ID to worker info
	workersLock      sync.Mutex

	// Directory tracking for locality-aware scheduling
	dirToWorkers     map[string]map[string]struct{} // map[directory]map[workerID]struct{}
	dirToWorkersLock sync.RWMutex
}

type workerInfo struct {
	id              string
	maxJobs         int
	activeJobs      int
	lastSeen        time.Time
	compilers       []string
	linkers         []string
	supportsLinking bool
	memoryMB        int32
	jobStream       pb.BuildService_WorkerStreamServer
	streamConnected bool
	recentDirs      []string // Recently processed directories
}

type jobStatusInfo struct {
	status      string // "pending", "assigned", "running", "completed", "failed"
	worker      string
	timeCreated time.Time
	timeStarted time.Time
	timeEnded   time.Time
	error       string
	attempts    int
	isLinkJob   bool // Whether this is a link job
}

// NewBuildServer creates a new instance of the build server
func NewBuildServer() *BuildServer {
	return &BuildServer{
		pendingJobs:      make(chan *pb.JobRequest, 10000), // Buffer for 10k jobs
		availableWorkers: make(chan string, 100),           // Buffer for 100 worker IDs
		workers:          make(map[string]workerInfo),
		jobStatus:        make(map[string]jobStatusInfo),
		compileJobs:      make(map[string]*pb.CompileJobRequest),
		linkJobs:         make(map[string]*pb.LinkJobRequest),
		dirToWorkers:     make(map[string]map[string]struct{}),
	}
}

// SubmitJob handles compilation job submission from clients
func (s *BuildServer) SubmitCompileJob(ctx context.Context, req *pb.CompileJobRequest) (*pb.CompileJobResponse, error) {
	// Generate a job ID if one wasn't provided
	if req.JobId == "" {
		req.JobId = uuid.New().String()
	}

	// Store job details
	s.jobDetailsLock.Lock()
	s.compileJobs[req.JobId] = req
	s.jobDetailsLock.Unlock()

	// Record job status
	s.jobStatusLock.Lock()
	s.jobStatus[req.JobId] = jobStatusInfo{
		status:      "pending",
		timeCreated: time.Now(),
		attempts:    0,
		isLinkJob:   false,
	}
	s.jobStatusLock.Unlock()

	// Wrap into a JobRequest and add to queue
	jobReq := &pb.JobRequest{
		Job: &pb.JobRequest_CompileJob{
			CompileJob: req,
		},
	}

	// Using non-blocking send with timeout to avoid deadlocks
	select {
	case s.pendingJobs <- jobReq:
		log.Printf("Compile job submitted: %s for file %s", req.JobId, req.SourceFile)
		return &pb.CompileJobResponse{
			JobId:    req.JobId,
			Accepted: true,
			Message:  "Job accepted and queued",
		}, nil
	case <-time.After(5 * time.Second):
		return &pb.CompileJobResponse{
			JobId:    req.JobId,
			Accepted: false,
			Message:  "Server queue is full, timed out waiting",
		}, status.Error(codes.ResourceExhausted, "Job queue is full and timed out")
	}
}

// SubmitLinkJob handles linking job submission from clients
func (s *BuildServer) SubmitLinkJob(ctx context.Context, req *pb.LinkJobRequest) (*pb.LinkJobResponse, error) {
	// Generate a job ID if one wasn't provided
	if req.JobId == "" {
		req.JobId = uuid.New().String()
	}

	// Store job details
	s.jobDetailsLock.Lock()
	s.linkJobs[req.JobId] = req
	s.jobDetailsLock.Unlock()

	// Record job status
	s.jobStatusLock.Lock()
	s.jobStatus[req.JobId] = jobStatusInfo{
		status:      "pending",
		timeCreated: time.Now(),
		attempts:    0,
		isLinkJob:   true,
	}
	s.jobStatusLock.Unlock()

	// Wrap into a JobRequest and add to queue
	jobReq := &pb.JobRequest{
		Job: &pb.JobRequest_LinkJob{
			LinkJob: req,
		},
	}

	// Using non-blocking send with timeout to avoid deadlocks
	select {
	case s.pendingJobs <- jobReq:
		log.Printf("Link job submitted: %s for output %s", req.JobId, req.OutputFile)
		return &pb.LinkJobResponse{
			JobId:    req.JobId,
			Accepted: true,
			Message:  "Link job accepted and queued",
		}, nil
	case <-time.After(5 * time.Second):
		return &pb.LinkJobResponse{
			JobId:    req.JobId,
			Accepted: false,
			Message:  "Server queue is full, timed out waiting",
		}, status.Error(codes.ResourceExhausted, "Job queue is full and timed out")
	}
}

// Batch job submission handlers
func (s *BuildServer) SubmitBatchCompileJobs(ctx context.Context, req *pb.BatchCompileJobRequest) (*pb.BatchCompileJobResponse, error) {
	responses := make([]*pb.CompileJobResponse, 0, len(req.Jobs))

	log.Printf("Received batch of %d compilation jobs", len(req.Jobs))

	for _, job := range req.Jobs {
		resp, err := s.SubmitCompileJob(ctx, job)
		if err != nil {
			// Create failure response
			responses = append(responses, &pb.CompileJobResponse{
				JobId:    job.JobId,
				Accepted: false,
				Message:  fmt.Sprintf("Failed to submit job: %v", err),
			})
		} else {
			responses = append(responses, resp)
		}
	}

	return &pb.BatchCompileJobResponse{
		Responses: responses,
	}, nil
}

func (s *BuildServer) SubmitBatchLinkJobs(ctx context.Context, req *pb.BatchLinkJobRequest) (*pb.BatchLinkJobResponse, error) {
	responses := make([]*pb.LinkJobResponse, 0, len(req.Jobs))

	log.Printf("Received batch of %d linking jobs", len(req.Jobs))

	for _, job := range req.Jobs {
		resp, err := s.SubmitLinkJob(ctx, job)
		if err != nil {
			// Create failure response
			responses = append(responses, &pb.LinkJobResponse{
				JobId:    job.JobId,
				Accepted: false,
				Message:  fmt.Sprintf("Failed to submit job: %v", err),
			})
		} else {
			responses = append(responses, resp)
		}
	}

	return &pb.BatchLinkJobResponse{
		Responses: responses,
	}, nil
}

// Directory update handler for locality-aware scheduling
func (s *BuildServer) UpdateDirectories(ctx context.Context, req *pb.DirectoryUpdateRequest) (*pb.DirectoryUpdateResponse, error) {
	workerId := req.WorkerId

	s.dirToWorkersLock.Lock()
	defer s.dirToWorkersLock.Unlock()

	// First, remove this worker from all directory mappings
	for dir, workers := range s.dirToWorkers {
		delete(workers, workerId)

		// Clean up empty sets
		if len(workers) == 0 {
			delete(s.dirToWorkers, dir)
		}
	}

	// Add worker to new directory mappings
	for _, dir := range req.Directories {
		workers, exists := s.dirToWorkers[dir]
		if !exists {
			workers = make(map[string]struct{})
			s.dirToWorkers[dir] = workers
		}
		workers[workerId] = struct{}{}
	}

	// Update worker's directory list
	s.workersLock.Lock()
	if worker, exists := s.workers[workerId]; exists {
		// Just store the list of directories
		worker.recentDirs = req.Directories
		s.workers[workerId] = worker
	}
	s.workersLock.Unlock()

	return &pb.DirectoryUpdateResponse{Success: true}, nil
}

// WorkerStream establishes a stream with a worker for receiving jobs
func (s *BuildServer) WorkerStream(reg *pb.WorkerRegistration, stream pb.BuildService_WorkerStreamServer) error {
	// Register worker
	s.workersLock.Lock()
	workerId := reg.WorkerId
	if workerId == "" {
		workerId = uuid.New().String()
	}

	// Store worker information
	s.workers[workerId] = workerInfo{
		id:              workerId,
		maxJobs:         int(reg.MaxJobs),
		activeJobs:      0,
		lastSeen:        time.Now(),
		compilers:       reg.SupportedCompilers,
		linkers:         reg.SupportedLinkers,
		supportsLinking: reg.SupportsLinking,
		memoryMB:        reg.MemoryMb,
		jobStream:       stream,
		streamConnected: true,
		recentDirs:      reg.RecentDirectories,
	}
	s.workersLock.Unlock()

	// Register directories for locality-aware scheduling if provided
	if len(reg.RecentDirectories) > 0 {
		s.dirToWorkersLock.Lock()
		for _, dir := range reg.RecentDirectories {
			workers, exists := s.dirToWorkers[dir]
			if !exists {
				workers = make(map[string]struct{})
				s.dirToWorkers[dir] = workers
			}
			workers[workerId] = struct{}{}
		}
		s.dirToWorkersLock.Unlock()
	}

	// Signal worker availability - use non-blocking send
	select {
	case s.availableWorkers <- workerId:
		// Successfully added to available workers
	default:
		log.Printf("WARNING: Could not add worker %s to available workers channel (channel full)", workerId)
		// Continue anyway, the worker health check will retry
	}

	log.Printf("Worker connected: %s (max jobs: %d, linking: %v, dirs: %d)",
		workerId, reg.MaxJobs, reg.SupportsLinking, len(reg.RecentDirectories))

	// Keep checking if the worker is still connected
	for {
		// Update last seen time
		s.workersLock.Lock()
		if worker, exists := s.workers[workerId]; exists {
			worker.lastSeen = time.Now()
			s.workers[workerId] = worker
			s.workersLock.Unlock()
		} else {
			s.workersLock.Unlock()
			return fmt.Errorf("worker %s no longer registered", workerId)
		}

		// Check for stream cancellation
		select {
		case <-stream.Context().Done():
			s.workersLock.Lock()
			if worker, exists := s.workers[workerId]; exists {
				worker.streamConnected = false
				s.workers[workerId] = worker

				// Requeue any jobs assigned to this worker
				go s.requeueWorkerJobs(workerId)
			}
			s.workersLock.Unlock()
			log.Printf("Worker disconnected: %s", workerId)
			return nil
		case <-time.After(5 * time.Second):
			// Periodic health check
			s.workersLock.Lock()
			worker, exists := s.workers[workerId]
			if !exists || !worker.streamConnected {
				s.workersLock.Unlock()
				return fmt.Errorf("worker %s marked as disconnected", workerId)
			}

			// If worker has capacity, make it available again
			if worker.activeJobs < worker.maxJobs {
				s.workersLock.Unlock()
				// Non-blocking send to the available workers channel
				select {
				case s.availableWorkers <- workerId:
					// Successfully added to available workers
				default:
					// Channel is full, that's fine
					log.Printf("WARNING: Could not add worker %s to available workers channel (channel full)", workerId)
				}
			} else {
				s.workersLock.Unlock()
			}
		}
	}
}

// requeueWorkerJobs requeues all jobs assigned to a worker that has disconnected
func (s *BuildServer) requeueWorkerJobs(workerId string) {
	// Find all jobs assigned to this worker
	s.jobStatusLock.Lock()
	jobsToRequeue := make([]string, 0)

	for jobId, jobInfo := range s.jobStatus {
		if jobInfo.worker == workerId && (jobInfo.status == "assigned" || jobInfo.status == "running") {
			jobsToRequeue = append(jobsToRequeue, jobId)

			// Update job status to pending
			jobInfo.status = "pending"
			// Keep track of previous failed attempts
			jobInfo.attempts++
			s.jobStatus[jobId] = jobInfo
		}
	}
	s.jobStatusLock.Unlock()

	// Requeue each job
	for _, jobId := range jobsToRequeue {
		s.requeueJob(jobId)
	}

	log.Printf("Requeued %d jobs from disconnected worker %s", len(jobsToRequeue), workerId)
}

// requeueJob reconstructs and requeues a job using stored complete details
func (s *BuildServer) requeueJob(jobId string) {
	// Check if this is a compile or link job
	s.jobStatusLock.RLock()
	jobInfo, exists := s.jobStatus[jobId]
	isLinkJob := false
	if exists {
		isLinkJob = jobInfo.isLinkJob
	}
	s.jobStatusLock.RUnlock()

	if !exists {
		log.Printf("Cannot requeue job %s: job info not found", jobId)
		return
	}

	var jobReq *pb.JobRequest

	// Get full job details from storage
	s.jobDetailsLock.RLock()
	if isLinkJob {
		linkJob, exists := s.linkJobs[jobId]
		if exists {
			jobReq = &pb.JobRequest{
				Job: &pb.JobRequest_LinkJob{
					LinkJob: linkJob,
				},
			}
		}
	} else {
		compileJob, exists := s.compileJobs[jobId]
		if exists {
			jobReq = &pb.JobRequest{
				Job: &pb.JobRequest_CompileJob{
					CompileJob: compileJob,
				},
			}
		}
	}
	s.jobDetailsLock.RUnlock()

	if jobReq == nil {
		log.Printf("Cannot requeue job %s: job details not found", jobId)
		return
	}

	// Add job back to the queue using non-blocking send with retry
	log.Printf("Requeueing job %s (attempt %d)", jobId, jobInfo.attempts)

	// Try to requeue the job with a non-blocking send first
	select {
	case s.pendingJobs <- jobReq:
		log.Printf("Job %s requeued successfully", jobId)
	default:
		// If channel is full, try in a goroutine with retries
		go func(req *pb.JobRequest, id string) {
			for retries := 0; retries < 5; retries++ {
				select {
				case s.pendingJobs <- req:
					log.Printf("Job %s requeued successfully after %d retries", id, retries)
					return
				case <-time.After(time.Duration(500*(retries+1)) * time.Millisecond):
					log.Printf("Failed to requeue job %s (channel full), retry %d", id, retries+1)
				}
			}
			log.Printf("WARNING: Failed to requeue job %s after 5 attempts, job may be lost", id)
		}(jobReq, jobId)
	}
}

// ReportJobStatus receives job completion status from workers
func (s *BuildServer) ReportJobStatus(ctx context.Context, report *pb.JobStatusReport) (*pb.JobStatusAck, error) {
	s.jobStatusLock.Lock()

	// Update job status
	if jobInfo, exists := s.jobStatus[report.JobId]; exists {
		jobInfo.status = "completed"
		if !report.Success {
			jobInfo.status = "failed"
			jobInfo.error = report.ErrorMessage
		}
		jobInfo.timeEnded = time.Now()
		jobInfo.worker = report.WorkerId
		jobInfo.isLinkJob = report.IsLinkJob
		s.jobStatus[report.JobId] = jobInfo

		// Clean up job details if completed successfully to save memory
		if report.Success {
			go func(jobId string, isLinkJob bool) {
				// Wait a bit before cleanup to allow for potential status queries
				time.Sleep(5 * time.Minute)

				s.jobDetailsLock.Lock()
				if isLinkJob {
					delete(s.linkJobs, jobId)
				} else {
					delete(s.compileJobs, jobId)
				}
				s.jobDetailsLock.Unlock()
			}(report.JobId, report.IsLinkJob)
		} else if jobInfo.attempts < 3 {
			// If job failed but hasn't exceeded max attempts, requeue it
			go s.requeueJob(report.JobId)
		}

		s.jobStatusLock.Unlock()

		// Update worker job count
		s.workersLock.Lock()
		if worker, exists := s.workers[report.WorkerId]; exists {
			worker.activeJobs--
			if worker.activeJobs < 0 {
				worker.activeJobs = 0
			}
			s.workers[report.WorkerId] = worker

			// Make worker available again
			if worker.streamConnected && worker.activeJobs < worker.maxJobs {
				select {
				case s.availableWorkers <- report.WorkerId:
					// Successfully added to available workers
					log.Printf("Worker %s added back to available workers (jobs: %d/%d)",
						report.WorkerId, worker.activeJobs, worker.maxJobs)
				default:
					// Channel full, log a warning
					log.Printf("WARNING: Could not add worker %s to available workers channel (channel full)",
						report.WorkerId)
				}
			}
		}
		s.workersLock.Unlock()

		jobType := "compile"
		if report.IsLinkJob {
			jobType = "link"
		}
		log.Printf("%s job %s %s on worker %s",
			jobType, report.JobId, jobInfo.status, report.WorkerId)
	} else {
		s.jobStatusLock.Unlock()
		log.Printf("Unknown job reported: %s", report.JobId)
		return &pb.JobStatusAck{Received: false}, status.Error(codes.NotFound, "Job not found")
	}

	return &pb.JobStatusAck{Received: true}, nil
}

// GetJobStatus returns the current status of a job
func (s *BuildServer) GetJobStatus(ctx context.Context, req *pb.JobStatusRequest) (*pb.JobStatusResponse, error) {
	s.jobStatusLock.RLock()
	defer s.jobStatusLock.RUnlock()

	jobInfo, exists := s.jobStatus[req.JobId]
	if !exists {
		return nil, status.Error(codes.NotFound, "Job not found")
	}

	var elapsedTime int64 = 0
	if !jobInfo.timeStarted.IsZero() {
		if jobInfo.timeEnded.IsZero() {
			// Job is still running, calculate time from start until now
			elapsedTime = time.Since(jobInfo.timeStarted).Milliseconds()
		} else {
			// Job is done, calculate full execution time
			elapsedTime = jobInfo.timeEnded.Sub(jobInfo.timeStarted).Milliseconds()
		}
	}

	return &pb.JobStatusResponse{
		JobId:         req.JobId,
		Status:        jobInfo.status,
		ErrorMessage:  jobInfo.error,
		WorkerId:      jobInfo.worker,
		ElapsedTimeMs: elapsedTime,
		IsLinkJob:     jobInfo.isLinkJob,
	}, nil
}

// Helper to extract directories from job for locality-aware scheduling
func getJobDirectories(jobReq *pb.JobRequest) []string {
	var dirs []string

	switch job := jobReq.Job.(type) {
	case *pb.JobRequest_CompileJob:
		if job.CompileJob.SourceFile != "" {
			dirs = append(dirs, filepath.Dir(job.CompileJob.SourceFile))
		}
		if job.CompileJob.OutputFile != "" {
			dirs = append(dirs, filepath.Dir(job.CompileJob.OutputFile))
		}
		if job.CompileJob.WorkingDir != "" {
			dirs = append(dirs, job.CompileJob.WorkingDir)
		}
	case *pb.JobRequest_LinkJob:
		if job.LinkJob.OutputFile != "" {
			dirs = append(dirs, filepath.Dir(job.LinkJob.OutputFile))
		}
		if job.LinkJob.WorkingDir != "" {
			dirs = append(dirs, job.LinkJob.WorkingDir)
		}
		for _, input := range job.LinkJob.InputFiles {
			dirs = append(dirs, filepath.Dir(input))
		}
	}

	// Remove duplicates
	uniqueDirs := make(map[string]struct{})
	for _, dir := range dirs {
		uniqueDirs[dir] = struct{}{}
	}

	result := make([]string, 0, len(uniqueDirs))
	for dir := range uniqueDirs {
		result = append(result, dir)
	}

	return result
}

// findWorkersWithLocalityPreference returns workers sorted by locality preference
func (s *BuildServer) findWorkersWithLocalityPreference(dirs []string) []string {
	if len(dirs) == 0 {
		return nil
	}

	s.dirToWorkersLock.RLock()
	defer s.dirToWorkersLock.RUnlock()

	// Count worker occurrences across directories
	workerCounts := make(map[string]int)

	for _, dir := range dirs {
		if workers, exists := s.dirToWorkers[dir]; exists {
			for workerID := range workers {
				workerCounts[workerID]++
			}
		}
	}

	if len(workerCounts) == 0 {
		return nil
	}

	// Sort workers by locality score (most matching directories first)
	type workerScore struct {
		id    string
		score int
	}

	scores := make([]workerScore, 0, len(workerCounts))
	for id, count := range workerCounts {
		scores = append(scores, workerScore{id, count})
	}

	// Sort by score descending
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].score > scores[j].score
	})

	// Extract worker IDs in order
	result := make([]string, len(scores))
	for i, ws := range scores {
		result[i] = ws.id
	}

	return result
}

// StartScheduler starts the job scheduler routine with locality-aware scheduling
func (s *BuildServer) StartScheduler() {
	go func() {
		for jobReq := range s.pendingJobs {
			var jobId string
			var isLinkJob bool

			// Extract job ID and type based on job request type
			switch job := jobReq.Job.(type) {
			case *pb.JobRequest_CompileJob:
				jobId = job.CompileJob.JobId
				isLinkJob = false
			case *pb.JobRequest_LinkJob:
				jobId = job.LinkJob.JobId
				isLinkJob = true
			default:
				log.Printf("Unknown job type received, ignoring")
				continue
			}

			// Update job status if needed
			s.jobStatusLock.Lock()
			jobInfo, exists := s.jobStatus[jobId]
			if !exists {
				jobInfo = jobStatusInfo{
					status:      "pending",
					timeCreated: time.Now(),
					attempts:    0,
					isLinkJob:   isLinkJob,
				}
			}
			// Only update if job is not already completed or failed
			if jobInfo.status != "completed" && jobInfo.status != "failed" {
				jobInfo.status = "pending"
				s.jobStatus[jobId] = jobInfo
			} else {
				// Skip jobs that are already done
				s.jobStatusLock.Unlock()
				continue
			}
			s.jobStatusLock.Unlock()

			// Find an appropriate worker for this job
			var selectedWorker string

			// For link jobs, we need a worker that supports linking
			if isLinkJob {
				selectedWorker = s.findLinkCapableWorker()
			} else {
				// Use locality-aware scheduling for compilation jobs
				dirs := getJobDirectories(jobReq)
				localityWorkers := s.findWorkersWithLocalityPreference(dirs)

				// Try to use locality-preferred workers
				if len(localityWorkers) > 0 {
					for _, workerId := range localityWorkers {
						s.workersLock.Lock()
						worker, exists := s.workers[workerId]
						if exists && worker.streamConnected && worker.activeJobs < worker.maxJobs {
							// Found a suitable worker with locality preference
							worker.activeJobs++
							s.workers[workerId] = worker
							selectedWorker = workerId
							s.workersLock.Unlock()

							log.Printf("Using locality-preferred worker %s for job %s", workerId, jobId)
							break
						}
						s.workersLock.Unlock()
					}
				}

				// If no locality-preferred worker found, try to get any available worker from the channel
				if selectedWorker == "" {
					select {
					case selectedWorker = <-s.availableWorkers:
						// Got a worker from the channel
						s.workersLock.Lock()
						worker := s.workers[selectedWorker]
						worker.activeJobs++
						s.workers[selectedWorker] = worker
						s.workersLock.Unlock()
						log.Printf("Found available worker %s for job %s (no locality preference)", selectedWorker, jobId)
					case <-time.After(500 * time.Millisecond):
						// Timeout, no worker available
						log.Printf("No available worker for job %s, requeueing", jobId)

						// Requeue the job with a small delay to avoid tight loops
						go func(req *pb.JobRequest) {
							time.Sleep(1 * time.Second)
							select {
							case s.pendingJobs <- req:
								// Successfully requeued
							default:
								log.Printf("WARNING: Could not requeue job %s (channel full)", jobId)
								// Try one more time with longer delay
								time.Sleep(2 * time.Second)
								select {
								case s.pendingJobs <- req:
									// Successfully requeued on second attempt
								default:
									log.Printf("ERROR: Failed to requeue job %s after retry, job may be lost", jobId)
								}
							}
						}(jobReq)
						continue
					}
				}
			}

			if selectedWorker == "" {
				log.Printf("No suitable worker found for job %s, requeueing", jobId)
				// Requeue with delay to avoid tight loop
				go func(req *pb.JobRequest) {
					time.Sleep(1 * time.Second)
					select {
					case s.pendingJobs <- req:
						// Successfully requeued
					default:
						log.Printf("WARNING: Could not requeue job %s (channel full)", jobId)
					}
				}(jobReq)
				continue
			}

			// Double-check the selected worker is still available (might have changed since we checked for locality)
			s.workersLock.Lock()
			worker, exists := s.workers[selectedWorker]
			if !exists || !worker.streamConnected {
				s.workersLock.Unlock()
				// Worker no longer available, put job back in queue
				log.Printf("Selected worker %s is no longer available, requeueing job %s", selectedWorker, jobId)
				go func(req *pb.JobRequest) {
					time.Sleep(500 * time.Millisecond)
					select {
					case s.pendingJobs <- req:
						// Successfully requeued
					default:
						log.Printf("WARNING: Could not requeue job %s (channel full)", jobId)
					}
				}(jobReq)
				continue
			}

			// For link jobs, make sure worker supports linking
			if isLinkJob && !worker.supportsLinking {
				// Worker doesn't support linking, put job back and try another worker
				// Decrease the active job count since we're not going to use this worker
				worker.activeJobs--
				s.workers[selectedWorker] = worker
				s.workersLock.Unlock()

				log.Printf("Worker %s doesn't support linking, requeueing job %s", selectedWorker, jobId)
				go func(req *pb.JobRequest) {
					time.Sleep(500 * time.Millisecond)
					select {
					case s.pendingJobs <- req:
						// Successfully requeued
					default:
						log.Printf("WARNING: Could not requeue job %s (channel full)", jobId)
					}
				}(jobReq)
				continue
			}

			// At this point we have a worker whose activeJobs has already been incremented
			s.workersLock.Unlock()

			// Update job status
			s.jobStatusLock.Lock()
			jobInfo.status = "assigned"
			jobInfo.worker = selectedWorker
			jobInfo.timeStarted = time.Now()
			s.jobStatus[jobId] = jobInfo
			s.jobStatusLock.Unlock()

			// Send job to worker
			if err := worker.jobStream.Send(jobReq); err != nil {
				log.Printf("Failed to send job %s to worker %s: %v", jobId, selectedWorker, err)

				// Decrease worker's active job count
				s.workersLock.Lock()
				if w, exists := s.workers[selectedWorker]; exists {
					w.activeJobs--
					if w.activeJobs < 0 {
						w.activeJobs = 0
					}

					// If worker seems disconnected, mark it as such
					if status.Code(err) == codes.Unavailable {
						w.streamConnected = false
					} else {
						// Otherwise, make the worker available again
						select {
						case s.availableWorkers <- selectedWorker:
							// Successfully added back to available workers
						default:
							log.Printf("WARNING: Could not add worker %s back to available workers (channel full)", selectedWorker)
						}
					}
					s.workers[selectedWorker] = w
				}
				s.workersLock.Unlock()

				// Put job back in queue
				log.Printf("Requeueing job %s after failed send", jobId)
				go func(req *pb.JobRequest) {
					time.Sleep(500 * time.Millisecond)
					select {
					case s.pendingJobs <- req:
						// Successfully requeued
					default:
						log.Printf("WARNING: Could not requeue job %s (channel full)", jobId)
					}
				}(jobReq)
			} else {
				jobType := "compile"
				if isLinkJob {
					jobType = "link"
				}
				log.Printf("%s job %s assigned to worker %s (attempt %d)",
					jobType, jobId, selectedWorker, jobInfo.attempts)

				// Start a background goroutine to monitor this job for potential failure
				go s.monitorJob(jobId, selectedWorker, isLinkJob)
			}
		}
	}()
}

// findLinkCapableWorker finds a worker that can handle link jobs
// This is separate from the regular worker selection to prioritize
// workers with more resources for linking tasks
func (s *BuildServer) findLinkCapableWorker() string {
	// First try to get a worker from the available queue with timeout
	var workerId string

	select {
	case workerId = <-s.availableWorkers:
		// We got a worker from the queue, check if it supports linking
		s.workersLock.Lock()
		defer s.workersLock.Unlock()

		worker, exists := s.workers[workerId]
		if exists && worker.streamConnected && worker.supportsLinking {
			log.Printf("Found linking-capable worker %s from available queue", workerId)
			worker.activeJobs++
			s.workers[workerId] = worker
			return workerId
		}

		// This worker doesn't support linking, put it back in the queue
		// and try to find another one
		if exists && worker.streamConnected {
			// Non-blocking put back
			select {
			case s.availableWorkers <- workerId:
				// Successfully added back
			default:
				log.Printf("WARNING: Could not put worker %s back in available queue (full)", workerId)
			}
		}

		// Try to find a worker that supports linking directly in the map
		var bestWorker string
		var bestMemory int32 = 0

		for id, w := range s.workers {
			if w.streamConnected && w.supportsLinking && w.activeJobs < w.maxJobs {
				// Prefer workers with more memory for linking
				if w.memoryMB > bestMemory {
					bestWorker = id
					bestMemory = w.memoryMB
				}
			}
		}

		if bestWorker != "" {
			log.Printf("Found linking-capable worker %s with %d MB memory", bestWorker, bestMemory)
			worker = s.workers[bestWorker]
			worker.activeJobs++
			s.workers[bestWorker] = worker
		}
		return bestWorker

	case <-time.After(500 * time.Millisecond):
		// No workers available in queue, search directly in the workers map
		s.workersLock.Lock()
		defer s.workersLock.Unlock()

		var bestWorker string
		var bestMemory int32 = 0

		for id, w := range s.workers {
			if w.streamConnected && w.supportsLinking && w.activeJobs < w.maxJobs {
				// Prefer workers with more memory for linking
				if w.memoryMB > bestMemory {
					bestWorker = id
					bestMemory = w.memoryMB
				}
			}
		}

		if bestWorker != "" {
			log.Printf("Found linking-capable worker %s with %d MB memory (after timeout)", bestWorker, bestMemory)
			worker := s.workers[bestWorker]
			worker.activeJobs++
			s.workers[bestWorker] = worker
		} else {
			log.Printf("No linking-capable workers available with capacity")
		}
		return bestWorker
	}
}

// monitorJob checks if a job has completed within a reasonable time
// and requeues it if the worker appears to have failed
func (s *BuildServer) monitorJob(jobId, workerId string, isLinkJob bool) {
	// Wait for some time to give the job a chance to complete
	// For link jobs, allow more time as they're typically longer
	jobTimeout := 30 * time.Minute
	if isLinkJob {
		jobTimeout = 60 * time.Minute // Linking can take longer
	}

	timer := time.NewTimer(jobTimeout)
	defer timer.Stop()

	<-timer.C

	// Check if job is still in progress
	s.jobStatusLock.Lock()
	jobInfo, exists := s.jobStatus[jobId]
	if !exists || (jobInfo.status != "assigned" && jobInfo.status != "running") {
		// Job has completed or was never assigned, nothing to do
		s.jobStatusLock.Unlock()
		return
	}

	// Job is still assigned but hasn't completed - likely a worker failure
	log.Printf("Job %s on worker %s timed out after %v, requeueing", jobId, workerId, jobTimeout)

	// Reset job status to pending
	jobInfo.status = "pending"
	jobInfo.attempts++
	s.jobStatus[jobId] = jobInfo
	s.jobStatusLock.Unlock()

	// Requeue the job using stored details
	s.requeueJob(jobId)

	// Update worker status - mark it as having one fewer job
	s.workersLock.Lock()
	if worker, exists := s.workers[workerId]; exists {
		worker.activeJobs--
		if worker.activeJobs < 0 {
			worker.activeJobs = 0
		}
		s.workers[workerId] = worker
	}
	s.workersLock.Unlock()
}

// StartServer starts the gRPC server on the specified address
func StartServer(address string) (*grpc.Server, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("failed to listen: %v", err)
	}

	server := grpc.NewServer()
	buildServer := NewBuildServer()
	pb.RegisterBuildServiceServer(server, buildServer)

	// Start job scheduler
	buildServer.StartScheduler()

	go func() {
		log.Printf("Starting server on %s", address)
		if err := server.Serve(listener); err != nil {
			log.Fatalf("Failed to serve: %v", err)
		}
	}()

	return server, nil
}
