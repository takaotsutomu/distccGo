package compiler

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
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
}

// NewLinkerLauncher creates a new launcher instance for linking
func NewLinkerLauncher(fsMount, serverAddr, buildDir string) *LinkerLauncher {
	return &LinkerLauncher{
		fsMountPoint:  fsMount,
		serverAddress: serverAddr,
		buildDir:      buildDir,
	}
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

// Close closes the connection to the server
func (l *LinkerLauncher) Close() error {
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

	// Send job to build server and wait for completion
	return l.submitJob(job)
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
