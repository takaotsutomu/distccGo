package worker

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	pb "github.com/takaotsutomu/distccGo/internal/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Worker represents a build worker that processes compilation and linking jobs
type Worker struct {
	id              string
	maxJobs         int32
	serverAddress   string
	fsMountPoint    string
	compilers       []string
	linkers         []string
	supportsLinking bool
	memoryMB        int32
	cpuCount        int32

	client pb.BuildServiceClient
	conn   *grpc.ClientConn

	// Job tracking
	jobsWg         sync.WaitGroup
	activeJobs     int32
	activeJobsLock sync.Mutex
	jobMapLock     sync.Mutex
	jobMap         map[string]*JobInfo
}

// JobInfo tracks information about a currently processing job
type JobInfo struct {
	jobId     string
	isLinkJob bool
	cmd       *exec.Cmd
	startTime time.Time
	cancel    context.CancelFunc
}

// NewWorker creates a new worker instance with automatic resource detection
func NewWorker(serverAddr, fsMount string, maxJobs int32) *Worker {
	return &Worker{
		id:              uuid.New().String(),
		maxJobs:         maxJobs,
		serverAddress:   serverAddr,
		fsMountPoint:    fsMount,
		compilers:       GetAvailableCompilers(),
		linkers:         GetAvailableLinkers(),
		supportsLinking: true, // Enable by default
		memoryMB:        GetAvailableMemory(),
		cpuCount:        int32(runtime.NumCPU()),
		activeJobs:      0,
		jobMap:          make(map[string]*JobInfo),
	}
}

// SetSupportsLinking enables or disables linking support for this worker
func (w *Worker) SetSupportsLinking(supports bool) {
	w.supportsLinking = supports
	if !supports {
		log.Println("Linking support has been disabled")
	}
}

// Connect establishes a connection to the build server
func (w *Worker) Connect() error {
	var err error
	w.conn, err = grpc.Dial(w.serverAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("failed to connect to server: %v", err)
	}

	w.client = pb.NewBuildServiceClient(w.conn)
	return nil
}

// Start begins processing jobs from the server
func (w *Worker) Start(ctx context.Context) error {
	if w.client == nil {
		return fmt.Errorf("worker not connected to server")
	}

	// Initialize random seed for jittering
	rand.Seed(time.Now().UnixNano())

	log.Printf("Worker starting with ID: %s, Max Jobs: %d, Memory: %d MB, CPUs: %d",
		w.id, w.maxJobs, w.memoryMB, w.cpuCount)

	if w.supportsLinking {
		log.Printf("Worker supports linking with linkers: %v", w.linkers)
	} else {
		log.Printf("Worker does NOT support linking")
	}

	for {
		select {
		case <-ctx.Done():
			log.Printf("Context cancelled, worker shutting down: %v", ctx.Err())
			return ctx.Err()
		default:
			// Continue processing
		}

		// Register with server and create job stream
		stream, err := w.client.WorkerStream(ctx, &pb.WorkerRegistration{
			WorkerId:           w.id,
			MaxJobs:            w.maxJobs,
			SupportedCompilers: w.compilers,
			SupportedLinkers:   w.linkers,
			SupportsLinking:    w.supportsLinking,
			MemoryMb:           w.memoryMB,
		})

		if err != nil {
			log.Printf("Failed to create worker stream: %v, retrying in 5s", err)
			time.Sleep(5 * time.Second)
			continue
		}

		log.Printf("Connected to server %s, waiting for jobs", w.serverAddress)

		// Process jobs as they arrive
		for {
			jobReq, err := stream.Recv()
			if err != nil {
				log.Printf("Stream error: %v, reconnecting...", err)
				break // Break inner loop to reconnect
			}

			w.activeJobsLock.Lock()
			if w.activeJobs >= w.maxJobs {
				log.Printf("Worker at capacity (%d/%d jobs), rejecting new job",
					w.activeJobs, w.maxJobs)
				w.activeJobsLock.Unlock()

				// Send a job failure report to let the server know we're overloaded
				// Extract job ID based on the job type
				var jobId string
				var isLinkJob bool

				switch job := jobReq.Job.(type) {
				case *pb.JobRequest_CompileJob:
					jobId = job.CompileJob.JobId
					isLinkJob = false
				case *pb.JobRequest_LinkJob:
					jobId = job.LinkJob.JobId
					isLinkJob = true
				default:
					log.Printf("Unknown job type received, cannot report failure")
					continue
				}

				// Report the status as failed due to capacity
				jobStatusCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_, reportErr := w.client.ReportJobStatus(jobStatusCtx, &pb.JobStatusReport{
					JobId:        jobId,
					Success:      false,
					ErrorMessage: fmt.Sprintf("Worker %s is at capacity (%d/%d jobs)", w.id, w.activeJobs, w.maxJobs),
					WorkerId:     w.id,
					IsLinkJob:    isLinkJob,
				})
				cancel()

				if reportErr != nil {
					log.Printf("Failed to report capacity overload for job %s: %v", jobId, reportErr)
				}

				continue
			}
			w.activeJobs++
			w.activeJobsLock.Unlock()

			w.jobsWg.Add(1)

			// Determine job type and process accordingly
			switch job := jobReq.Job.(type) {
			case *pb.JobRequest_CompileJob:
				log.Printf("Received compile job for %s", job.CompileJob.SourceFile)
				go w.processCompileJob(ctx, job.CompileJob)
			case *pb.JobRequest_LinkJob:
				log.Printf("Received link job for %s", job.LinkJob.OutputFile)
				go w.processLinkJob(ctx, job.LinkJob)
			default:
				log.Printf("Unknown job type received, ignoring")
				w.activeJobsLock.Lock()
				w.activeJobs--
				w.activeJobsLock.Unlock()
				w.jobsWg.Done()
			}
		}

		// If we got here, the stream was broken - wait before reconnecting
		log.Printf("Connection to server lost, will reconnect in 5 seconds...")
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
			// Wait before reconnecting
		}
	}
}

// Close shuts down the worker, waiting for active jobs to complete
func (w *Worker) Close() error {
	log.Println("Worker shutting down, waiting for jobs to complete...")

	// Cancel all running jobs
	w.jobMapLock.Lock()
	numJobs := len(w.jobMap)
	for jobId, jobInfo := range w.jobMap {
		log.Printf("Cancelling job %s", jobId)
		jobInfo.cancel()
	}
	w.jobMapLock.Unlock()

	// Wait with timeout for jobs to complete
	waitCh := make(chan struct{})
	go func() {
		w.jobsWg.Wait()
		close(waitCh)
	}()

	select {
	case <-waitCh:
		log.Printf("All %d jobs completed successfully", numJobs)
	case <-time.After(10 * time.Second):
		log.Printf("Timeout waiting for %d jobs to complete", numJobs)
	}

	if w.conn != nil {
		return w.conn.Close()
	}
	return nil
}

// normalizeAndCheckPath ensures paths are properly handled with mount points
func (w *Worker) normalizeAndCheckPath(path string) string {
	// First normalize the path to remove extra slashes and resolve . and .. segments
	normalizedPath := filepath.Clean(path)
	
	// If path is already absolute and contains the mount point, use it as-is
	if filepath.IsAbs(normalizedPath) && strings.HasPrefix(normalizedPath, w.fsMountPoint) {
		return normalizedPath
	}
	
	// If path is absolute but doesn't contain the mount point
	if filepath.IsAbs(normalizedPath) {
		// Check if this is a system path that should remain unchanged
		systemPaths := []string{"/usr", "/bin", "/lib", "/etc", "/opt", "/sbin"}
		for _, sysPath := range systemPaths {
			if strings.HasPrefix(normalizedPath, sysPath) {
				return normalizedPath
			}
		}
		
		// For non-system absolute paths, convert them to be relative to the mount point
		relativePath := strings.TrimPrefix(normalizedPath, "/")
		mappedPath := filepath.Join(w.fsMountPoint, relativePath)
		return mappedPath
	}
	
	// For relative paths, join with the mount point
	return filepath.Join(w.fsMountPoint, normalizedPath)
}

// processCompileJob handles the execution of a compilation job
func (w *Worker) processCompileJob(parentCtx context.Context, job *pb.CompileJobRequest) {
	ctx, cancel := context.WithCancel(parentCtx)
	startTime := time.Now()

	// Register job in our tracking map
	w.jobMapLock.Lock()
	w.jobMap[job.JobId] = &JobInfo{
		jobId:     job.JobId,
		isLinkJob: false,
		startTime: startTime,
		cancel:    cancel,
	}
	w.jobMapLock.Unlock()

	// Ensure cleanup happens
	defer func() {
		// Remove job from tracking map
		w.jobMapLock.Lock()
		delete(w.jobMap, job.JobId)
		w.jobMapLock.Unlock()

		// Decrement active jobs
		w.activeJobsLock.Lock()
		w.activeJobs--
		w.activeJobsLock.Unlock()

		cancel()        // Cancel the context
		w.jobsWg.Done() // Mark job as done
	}()

	log.Printf("Processing compile job %s: %s", job.JobId, job.SourceFile)

	// Handle paths properly by checking if they already contain the mount point
	sourceFile := w.normalizeAndCheckPath(job.SourceFile)
	workingDir := w.normalizeAndCheckPath(job.WorkingDir)

	// For the output file, we need to be careful about relative paths
	var outputFile string
	if filepath.IsAbs(job.OutputFile) {
		outputFile = w.normalizeAndCheckPath(job.OutputFile)
	} else {
		// If output is relative, keep it relative to the working directory
		outputFile = job.OutputFile
	}

	// Make sure the working directory exists
	if err := os.MkdirAll(workingDir, 0755); err != nil {
		execTime := time.Since(startTime)
		w.reportStatus(job.JobId, false, fmt.Sprintf("Failed to create working directory: %v", err), execTime.Milliseconds(), false)
		return
	}

	// Check if source file exists
	if _, err := os.Stat(sourceFile); os.IsNotExist(err) {
		execTime := time.Since(startTime)
		w.reportStatus(job.JobId, false, fmt.Sprintf("Source file not found: %s", sourceFile), execTime.Milliseconds(), false)
		return
	}

	// Prepare compilation command
	compiler := job.Compiler
	if compiler == "" {
		// Determine compiler based on file extension
		ext := filepath.Ext(job.SourceFile)
		if ext == ".cpp" || ext == ".cc" || ext == ".cxx" {
			compiler = "g++"
		} else {
			compiler = "gcc"
		}
	}

	// Create output directory if needed (based on working directory)
	if filepath.IsAbs(outputFile) {
		// For absolute output paths, ensure directory exists
		if err := os.MkdirAll(filepath.Dir(outputFile), 0755); err != nil {
			execTime := time.Since(startTime)
			w.reportStatus(job.JobId, false, fmt.Sprintf("Failed to create output directory: %v", err), execTime.Milliseconds(), false)
			return
		}
	} else {
		// For relative paths, ensure the directory exists relative to working dir
		outDir := filepath.Join(workingDir, filepath.Dir(outputFile))
		if err := os.MkdirAll(outDir, 0755); err != nil {
			execTime := time.Since(startTime)
			w.reportStatus(job.JobId, false, fmt.Sprintf("Failed to create output directory: %v", err), execTime.Milliseconds(), false)
			return
		}
	}

	// Use the relative or absolute output path directly with compiler
	args := []string{"-c", sourceFile, "-o", outputFile}
	args = append(args, job.Args...)

	cmd := exec.CommandContext(ctx, compiler, args...)
	cmd.Dir = workingDir

	// Store cmd in our job info
	w.jobMapLock.Lock()
	if jobInfo, exists := w.jobMap[job.JobId]; exists {
		jobInfo.cmd = cmd
	}
	w.jobMapLock.Unlock()

	// Capture stdout and stderr
	output, err := cmd.CombinedOutput()
	execTime := time.Since(startTime)

	// Check if the context was cancelled
	if ctx.Err() != nil {
		w.reportStatus(job.JobId, false, fmt.Sprintf("Job cancelled: %v", ctx.Err()), execTime.Milliseconds(), false)
		return
	}

	// Report status back to server
	if err != nil {
		log.Printf("Compilation failed for job %s: %v\nOutput: %s", job.JobId, err, string(output))
		w.reportStatus(job.JobId, false, fmt.Sprintf("Compilation failed: %v\n%s", err, string(output)), execTime.Milliseconds(), false)
		return
	}

	log.Printf("Compile job %s completed in %v", job.JobId, execTime)
	w.reportStatus(job.JobId, true, "", execTime.Milliseconds(), false)
}

// processLinkJob handles the execution of a linking job
func (w *Worker) processLinkJob(parentCtx context.Context, job *pb.LinkJobRequest) {
	ctx, cancel := context.WithCancel(parentCtx)
	startTime := time.Now()

	// Register job in our tracking map
	w.jobMapLock.Lock()
	w.jobMap[job.JobId] = &JobInfo{
		jobId:     job.JobId,
		isLinkJob: true,
		startTime: startTime,
		cancel:    cancel,
	}
	w.jobMapLock.Unlock()

	// Ensure cleanup happens
	defer func() {
		// Remove job from tracking map
		w.jobMapLock.Lock()
		delete(w.jobMap, job.JobId)
		w.jobMapLock.Unlock()

		// Decrement active jobs
		w.activeJobsLock.Lock()
		w.activeJobs--
		w.activeJobsLock.Unlock()

		cancel()        // Cancel the context
		w.jobsWg.Done() // Mark job as done
	}()

	log.Printf("Processing link job %s: %s", job.JobId, job.OutputFile)

	workingDir := w.normalizeAndCheckPath(job.WorkingDir)

	var outputFile string
	if filepath.IsAbs(job.OutputFile) {
		outputFile = w.normalizeAndCheckPath(job.OutputFile)
	} else {
		// If output is relative, keep it relative to the working directory
		outputFile = job.OutputFile
	}

	// Normalize input files while preserving relative paths if needed
	normalizedInputFiles := make([]string, len(job.InputFiles))
	for i, inputFile := range job.InputFiles {
		if filepath.IsAbs(inputFile) {
			normalizedInputFiles[i] = w.normalizeAndCheckPath(inputFile)
		} else {
			// Keep relative paths as they are
			normalizedInputFiles[i] = inputFile
		}
	}

	// Make sure the working directory exists
	if err := os.MkdirAll(workingDir, 0755); err != nil {
		execTime := time.Since(startTime)
		w.reportStatus(job.JobId, false, fmt.Sprintf("Failed to create working directory: %v", err), execTime.Milliseconds(), true)
		return
	}

	// Create output directory if needed (based on working directory)
	if filepath.IsAbs(outputFile) {
		// For absolute output paths, ensure directory exists
		if err := os.MkdirAll(filepath.Dir(outputFile), 0755); err != nil {
			execTime := time.Since(startTime)
			w.reportStatus(job.JobId, false, fmt.Sprintf("Failed to create output directory: %v", err), execTime.Milliseconds(), true)
			return
		}
	} else {
		// For relative paths, ensure the directory exists relative to working dir
		outDir := filepath.Join(workingDir, filepath.Dir(outputFile))
		if err := os.MkdirAll(outDir, 0755); err != nil {
			execTime := time.Since(startTime)
			w.reportStatus(job.JobId, false, fmt.Sprintf("Failed to create output directory: %v", err), execTime.Milliseconds(), true)
			return
		}
	}

	// Check that all input files exist
	for i, inputFile := range normalizedInputFiles {
		var fileToCheck string
		if filepath.IsAbs(inputFile) {
			fileToCheck = inputFile
		} else {
			fileToCheck = filepath.Join(workingDir, inputFile)
		}

		if _, err := os.Stat(fileToCheck); os.IsNotExist(err) {
			execTime := time.Since(startTime)
			w.reportStatus(job.JobId, false, fmt.Sprintf("Input file not found: %s (original: %s)",
				fileToCheck, job.InputFiles[i]), execTime.Milliseconds(), true)
			return
		}
	}

	// Prepare linking command - ensure we have a valid linker
	linker := job.Linker
	if linker == "" {
		linker = "ld" // Default linker
	}

	// Use the original arguments but with correct input/output paths
	cmdArgs := job.Args

	cmd := exec.CommandContext(ctx, linker, cmdArgs...)
	cmd.Dir = workingDir

	// Store cmd in our job info
	w.jobMapLock.Lock()
	if jobInfo, exists := w.jobMap[job.JobId]; exists {
		jobInfo.cmd = cmd
	}
	w.jobMapLock.Unlock()

	// Capture stdout and stderr
	output, err := cmd.CombinedOutput()
	execTime := time.Since(startTime)

	// Check if the context was cancelled
	if ctx.Err() != nil {
		w.reportStatus(job.JobId, false, fmt.Sprintf("Link job cancelled: %v", ctx.Err()), execTime.Milliseconds(), true)
		return
	}

	// Report status back to server
	if err != nil {
		log.Printf("Linking failed for job %s: %v\nOutput: %s", job.JobId, err, string(output))
		w.reportStatus(job.JobId, false, fmt.Sprintf("Linking failed: %v\n%s", err, string(output)), execTime.Milliseconds(), true)
		return
	}

	log.Printf("Link job %s completed in %v", job.JobId, execTime)
	w.reportStatus(job.JobId, true, "", execTime.Milliseconds(), true)
}

// reportStatus sends job status back to the server with retries
func (w *Worker) reportStatus(jobId string, success bool, errorMsg string, executionTimeMs int64, isLinkJob bool) {
	// Use a background context for reporting status
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	maxRetries := 5
	backoff := 500 * time.Millisecond

	var lastErr error
	for i := 0; i < maxRetries; i++ {
		_, err := w.client.ReportJobStatus(ctx, &pb.JobStatusReport{
			JobId:           jobId,
			Success:         success,
			ErrorMessage:    errorMsg,
			WorkerId:        w.id,
			ExecutionTimeMs: executionTimeMs,
			IsLinkJob:       isLinkJob,
		})

		if err == nil {
			jobType := "compile"
			if isLinkJob {
				jobType = "link"
			}
			status := "succeeded"
			if !success {
				status = "failed"
			}
			log.Printf("Successfully reported %s job %s status: %s", jobType, jobId, status)
			return // Successfully reported
		}

		lastErr = err
		log.Printf("Failed to report status for job %s (attempt %d/%d): %v",
			jobId, i+1, maxRetries, err)

		// Exponential backoff with jitter
		jitter := time.Duration(float64(backoff) * (0.5 + float64(rand.Intn(100))/100.0))
		time.Sleep(jitter)
		backoff *= 2
	}

	log.Printf("Failed to report job %s status after %d attempts: %v",
		jobId, maxRetries, lastErr)
}
