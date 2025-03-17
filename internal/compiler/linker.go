package compiler

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	pb "github.com/takaotsutomu/distccGo/internal/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// LinkerLauncher is a linker launcher that distributes linking jobs to the build server
type LinkerLauncher struct {
	fsMountPoint  string
	serverAddress string
	buildDir      string
	client        pb.BuildServiceClient
	conn          *grpc.ClientConn

	// Fields for job batching
	batchSize     int
	batchTimeout  time.Duration
	jobQueue      []*pb.LinkJobRequest
	jobQueueMutex sync.Mutex
	batchTimer    *time.Timer
	jobWaitGroup  sync.WaitGroup

	// Map to track job status and results
	jobResults     map[string]jobResult
	jobResultsLock sync.Mutex
}

// NewLinkerLauncher creates a new launcher instance for linking
func NewLinkerLauncher(fsMount, serverAddr, buildDir string, batchSize int, batchTimeout time.Duration) *LinkerLauncher {
	l := &LinkerLauncher{
		fsMountPoint:  fsMount,
		serverAddress: serverAddr,
		buildDir:      buildDir,
		batchSize:     batchSize,
		batchTimeout:  batchTimeout,
		jobQueue:      make([]*pb.LinkJobRequest, 0, batchSize),
		jobResults:    make(map[string]jobResult),
	}

	// Only create timer if batching is enabled
	if batchSize > 1 {
		l.batchTimer = time.AfterFunc(batchTimeout, func() {
			l.flushJobs()
		})
		l.batchTimer.Stop() // Don't start yet
	}

	return l
}

// Connect establishes a connection to the build server
func (l *LinkerLauncher) Connect() error {
	var err error
	l.conn, err = grpc.Dial(l.serverAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("failed to connect to server: %v", err)
	}

	l.client = pb.NewBuildServiceClient(l.conn)
	return nil
}

// Close closes the connection to the server and flushes any queued jobs
func (l *LinkerLauncher) Close() error {
	// Flush any queued jobs
	if l.batchSize > 1 {
		if err := l.flushJobs(); err != nil {
			log.Printf("Error flushing link jobs: %v", err)
		}
	}

	if l.conn != nil {
		return l.conn.Close()
	}
	return nil
}

// HandleLink processes a link request
func (l *LinkerLauncher) HandleLink(args []string) error {
	if l.client == nil {
		if err := l.Connect(); err != nil {
			return err
		}
		defer l.Close()
	}

	// Parse linking command
	job, err := l.parseArgs(args)
	if err != nil {
		return err
	}

	// If batching is disabled, use the original method
	if l.batchSize <= 1 {
		return l.submitJob(job)
	}

	// With batching enabled, add to queue and potentially flush
	l.jobWaitGroup.Add(1)

	// Register job in results map
	l.jobResultsLock.Lock()
	l.jobResults[job.JobId] = jobResult{
		completed: false,
		success:   false,
	}
	l.jobResultsLock.Unlock()

	defer l.jobWaitGroup.Done()

	// Add job to the queue
	l.jobQueueMutex.Lock()

	// Start the batch timer when the first job is added
	if len(l.jobQueue) == 0 && l.batchTimer != nil {
		l.batchTimer.Reset(l.batchTimeout)
	}

	l.jobQueue = append(l.jobQueue, job)
	queueFull := len(l.jobQueue) >= l.batchSize

	if queueFull {
		// If full, flush immediately (but don't hold the lock while doing so)
		l.jobQueueMutex.Unlock()
		if err := l.flushJobs(); err != nil {
			return fmt.Errorf("failed to flush link jobs: %v", err)
		}
	} else {
		l.jobQueueMutex.Unlock()
	}

	// Wait for job to be completed
	return l.waitForJobResult(job.JobId)
}

// parseArgs parses linker arguments to create a linking job
func (l *LinkerLauncher) parseArgs(args []string) (*pb.LinkJobRequest, error) {
	var outputFile string
	var inputFiles []string
	var otherArgs []string

	// First argument must be the linker executable
	if len(args) == 0 {
		return nil, fmt.Errorf("no linker specified")
	}
	linker := args[0]
	args = args[1:] // Remove linker from args

	// Extract output and input files from arguments
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-o" && i+1 < len(args):
			outputFile = args[i+1]
			otherArgs = append(otherArgs, args[i], args[i+1])
			i++
		case strings.HasSuffix(args[i], ".o") ||
			strings.HasSuffix(args[i], ".a") ||
			strings.HasSuffix(args[i], ".so"):
			// This is likely an input object file or library
			inputFiles = append(inputFiles, args[i])
			otherArgs = append(otherArgs, args[i])
		default:
			otherArgs = append(otherArgs, args[i])
		}
	}

	if outputFile == "" {
		return nil, fmt.Errorf("missing output file (no -o option)")
	}

	if len(inputFiles) == 0 {
		return nil, fmt.Errorf("no input object files or libraries specified")
	}

	var workingDir string
	if l.buildDir != "" {
		// Use the explicitly provided build directory
		workingDir = l.buildDir
	} else {
		// Fall back to output directory
		if filepath.IsAbs(outputFile) {
			workingDir = filepath.Dir(outputFile)
		} else if len(inputFiles) > 0 && filepath.IsAbs(inputFiles[0]) {
			workingDir = filepath.Dir(inputFiles[0])
		} else {
			workingDir = l.fsMountPoint // Last resort
		}
	}

	// Generate unique job ID
	jobId := uuid.New().String()

	// Create job request
	job := &pb.LinkJobRequest{
		JobId:      jobId,
		OutputFile: outputFile,
		InputFiles: inputFiles,
		Linker:     linker,
		Args:       otherArgs,
		WorkingDir: workingDir,
		Priority:   10, // Higher priority for link jobs
	}

	return job, nil
}

// submitJob sends the link job to the build server and waits for completion
func (l *LinkerLauncher) submitJob(job *pb.LinkJobRequest) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := l.client.SubmitLinkJob(ctx, job)
	if err != nil {
		return fmt.Errorf("failed to submit link job: %v", err)
	}

	if !resp.Accepted {
		return fmt.Errorf("link job rejected by server: %s", resp.Message)
	}

	log.Printf("Link job submitted successfully: %s", resp.JobId)

	// Wait for job completion
	return l.waitForJobCompletion(job.JobId)
}

// flushJobs sends all queued jobs as a batch
func (l *LinkerLauncher) flushJobs() error {
	l.jobQueueMutex.Lock()

	// Stop the timer
	if l.batchTimer != nil {
		l.batchTimer.Stop()
	}

	// If no jobs, nothing to do
	if len(l.jobQueue) == 0 {
		l.jobQueueMutex.Unlock()
		return nil
	}

	// Get jobs from queue
	jobs := make([]*pb.LinkJobRequest, len(l.jobQueue))
	copy(jobs, l.jobQueue)
	l.jobQueue = l.jobQueue[:0]

	l.jobQueueMutex.Unlock()

	// Submit the batch
	return l.submitBatchJobs(jobs)
}

// submitBatchJobs sends a batch of jobs to the server
func (l *LinkerLauncher) submitBatchJobs(jobs []*pb.LinkJobRequest) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	batchReq := &pb.BatchLinkJobRequest{
		Jobs: jobs,
	}

	log.Printf("Submitting batch of %d link jobs", len(jobs))

	resp, err := l.client.SubmitBatchLinkJobs(ctx, batchReq)
	if err != nil {
		// Mark all jobs as failed
		l.jobResultsLock.Lock()
		for _, job := range jobs {
			l.jobResults[job.JobId] = jobResult{
				completed: true,
				success:   false,
				errorMsg:  fmt.Sprintf("batch submission failed: %v", err),
			}
		}
		l.jobResultsLock.Unlock()

		return fmt.Errorf("failed to submit batch link jobs: %v", err)
	}

	// Process responses and update job results
	for i, jobResp := range resp.Responses {
		jobId := jobs[i].JobId

		l.jobResultsLock.Lock()
		if !jobResp.Accepted {
			// Job was rejected
			l.jobResults[jobId] = jobResult{
				completed: true,
				success:   false,
				errorMsg:  jobResp.Message,
			}
			log.Printf("Link job %s rejected: %s", jobId, jobResp.Message)
		} else {
			log.Printf("Link job %s accepted, waiting for completion", jobId)
			// Start a goroutine to wait for job completion
			go func(id string) {
				err := l.waitForJobCompletion(id)

				l.jobResultsLock.Lock()
				if err != nil {
					l.jobResults[id] = jobResult{
						completed: true,
						success:   false,
						errorMsg:  err.Error(),
					}
				} else {
					l.jobResults[id] = jobResult{
						completed: true,
						success:   true,
					}
				}
				l.jobResultsLock.Unlock()
			}(jobId)
		}
		l.jobResultsLock.Unlock()
	}

	return nil
}

// waitForJobCompletion polls the server until the job is complete
func (l *LinkerLauncher) waitForJobCompletion(jobId string) error {
	log.Printf("Waiting for link job %s to complete...", jobId)

	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

		status, err := l.client.GetJobStatus(ctx, &pb.JobStatusRequest{JobId: jobId})
		if err != nil {
			cancel()
			return fmt.Errorf("failed to get job status: %v", err)
		}

		switch status.Status {
		case "completed":
			cancel()
			log.Printf("Link job %s completed successfully", jobId)
			return nil
		case "failed":
			cancel()
			return fmt.Errorf("link job %s failed: %s", jobId, status.ErrorMessage)
		case "pending", "assigned", "running":
			// Job is still in progress, wait before checking again
			log.Printf("Link job %s status: %s", jobId, status.Status)
			cancel()
			time.Sleep(1 * time.Second)
			continue
		default:
			cancel()
			return fmt.Errorf("unknown job status: %s", status.Status)
		}
	}
}

// waitForJobResult waits for a specific job to be completed in the results map
func (l *LinkerLauncher) waitForJobResult(jobId string) error {
	for {
		l.jobResultsLock.Lock()
		result, ok := l.jobResults[jobId]
		if ok && result.completed {
			// Job is done, remove from tracking
			delete(l.jobResults, jobId)
			l.jobResultsLock.Unlock()

			if !result.success {
				return fmt.Errorf(result.errorMsg)
			}
			return nil
		}
		l.jobResultsLock.Unlock()

		// Wait and check again
		time.Sleep(100 * time.Millisecond)
	}
}
