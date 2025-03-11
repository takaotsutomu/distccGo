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

// CompilerLauncher is a compiler CompilerLauncher that distributes compilation jobs to the build server
type CompilerLauncher struct {
	fsMountPoint  string
	serverAddress string
	buildDir      string
	client        pb.BuildServiceClient
	conn          *grpc.ClientConn
}

// NewCompilerLauncher creates a new CompilerLauncher instance
func NewCompilerLauncher(fsMount, serverAddr, buildDir string) *CompilerLauncher {
	return &CompilerLauncher{
		fsMountPoint:  fsMount,
		serverAddress: serverAddr,
		buildDir:      buildDir,
	}
}

// Connect establishes a connection to the build server
func (l *CompilerLauncher) Connect() error {
	var err error
	l.conn, err = grpc.Dial(l.serverAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("failed to connect to server: %v", err)
	}

	l.client = pb.NewBuildServiceClient(l.conn)
	return nil
}

// Close closes the connection to the server
func (l *CompilerLauncher) Close() error {
	if l.conn != nil {
		return l.conn.Close()
	}
	return nil
}

// HandleCompile processes a compilation request
func (l *CompilerLauncher) HandleCompile(args []string) error {
	if l.client == nil {
		if err := l.Connect(); err != nil {
			return err
		}
		defer l.Close()
	}

	// Parse compilation command
	job, err := l.parseArgs(args)
	if err != nil {
		return err
	}

	// Send job to build server and wait for completion
	return l.submitJob(job)
}

// parseArgs parses compiler arguments to create a compilation job
func (l *CompilerLauncher) parseArgs(args []string) (*pb.CompileJobRequest, error) {
	var sourceFile, outputFile string
	var otherArgs []string

	// First argument must be the compiler executable
	if len(args) == 0 {
		return nil, fmt.Errorf("no compiler specified")
	}
	compiler := args[0]
	args = args[1:] // Remove compiler from args

	// Extract source and output files from the rest of the args
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-c" && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-"):
			sourceFile = args[i+1]
			i++
		case args[i] == "-o" && i+1 < len(args):
			outputFile = args[i+1]
			i++
		default:
			otherArgs = append(otherArgs, args[i])
		}
	}

	if sourceFile == "" {
		return nil, fmt.Errorf("missing source file (-c option)")
	}

	if outputFile == "" {
		// Infer output file if not specified
		base := filepath.Base(sourceFile)
		ext := filepath.Ext(base)
		outputFile = strings.TrimSuffix(base, ext) + ".o"
	}

	var workingDir string
	if l.buildDir != "" {
		// Use the explicitly provided build directory
		workingDir = l.buildDir
	} else {
		// Fall back to source file directory
		workingDir = filepath.Dir(sourceFile)
	}

	// Generate unique job ID
	jobId := uuid.New().String()

	// Create job request
	job := &pb.CompileJobRequest{
		JobId:      jobId,
		SourceFile: sourceFile,
		OutputFile: outputFile,
		Compiler:   compiler,
		Args:       otherArgs,
		WorkingDir: workingDir,
	}

	return job, nil
}

// submitJob sends the job to the build server and waits for completion
func (l *CompilerLauncher) submitJob(job *pb.CompileJobRequest) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := l.client.SubmitCompileJob(ctx, job)
	if err != nil {
		return fmt.Errorf("failed to submit job: %v", err)
	}

	if !resp.Accepted {
		return fmt.Errorf("job rejected by server: %s", resp.Message)
	}

	log.Printf("Job submitted successfully: %s", resp.JobId)

	// Wait for job completion
	return l.waitForJobCompletion(job.JobId)
}

// waitForJobCompletion polls the server until the job is complete
func (l *CompilerLauncher) waitForJobCompletion(jobId string) error {
	log.Printf("Waiting for job %s to complete...", jobId)

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
			log.Printf("Job %s completed successfully", jobId)
			return nil
		case "failed":
			cancel()
			return fmt.Errorf("job %s failed: %s", jobId, status.ErrorMessage)
		case "pending", "assigned", "running":
			// Job is still in progress, wait before checking again
			log.Printf("Job %s status: %s", jobId, status.Status)
			cancel()
			time.Sleep(1 * time.Second)
			continue
		default:
			cancel()
			return fmt.Errorf("unknown job status: %s", status.Status)
		}
	}
}
