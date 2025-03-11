package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/takaotsutomu/distccGo/internal/compiler"
	"github.com/takaotsutomu/distccGo/internal/server"
	"github.com/takaotsutomu/distccGo/internal/worker"
)

// Main entry point for the distributed builder system
// Can run in four modes:
// 1. Compiler Launcher Mode: Intercepts compilation commands and distributes them
// 2. Linker Launcher Mode: Intercepts linking commands and distributes them
// 3. Server Mode: Coordinates distributed build jobs
// 4. Worker Mode: Executes compilation and linking jobs

func main() {
	// Command line flags
	compilerMode := flag.Bool("compiler", false, "Run in compiler launcher mode")
	linkerMode := flag.Bool("linker", false, "Run in linker launcher mode")
	serverMode := flag.Bool("server", false, "Run in server mode")
	workerMode := flag.Bool("worker", false, "Run in worker mode")

	// Common settings
	serverAddr := flag.String("server-addr", "localhost:50051", "Build server address")
	fsMount := flag.String("fs-mount", "/mnt/cephfs", "Path to the shared filesystem mount point")
	buildDir := flag.String("build-dir", "", "Path to the build directory (root of build outputs)")

	// Worker specific settings
	maxJobs := flag.Int("max-jobs", 4, "Maximum parallel jobs for worker")
	disableLinking := flag.Bool("disable-linking", false, "Disable linking support for this worker")

	flag.Parse()

	// Validate command mode
	if !*compilerMode && !*linkerMode && !*serverMode && !*workerMode {
		log.Fatal("You must specify a mode: --launcher, --linker, --server, or --worker")
	}

	if *compilerMode {
		// Launcher/compiler mode - distributes compilation jobs
		compiler := compiler.NewCompilerLauncher(*fsMount, *serverAddr, *buildDir)

		if err := compiler.Connect(); err != nil {
			log.Fatalf("Failed to connect to server: %v", err)
		}
		defer compiler.Close()

		// Look for the "--" separator which indicates the start of the compiler command
		compilerCmd := []string{}
		separatorFound := false
		for i, arg := range os.Args {
			if arg == "--" && i+1 < len(os.Args) {
				// We found the separator, pass all remaining args excluding the "--" separator
				compilerCmd = os.Args[i+1:] 
				separatorFound = true
				break
			}
		}

		if !separatorFound || len(compilerCmd) < 1 {
			log.Fatal("No compilation command provided. Use -- to separate distcc-go options from the compiler command")
		}

		if err := compiler.HandleCompile(compilerCmd); err != nil {
			log.Fatalf("Compilation failed: %v", err)
		}
	} else if *linkerMode {
		// Linker launcher mode - distributes linking jobs
		linker := compiler.NewLinkerLauncher(*fsMount, *serverAddr, *buildDir)

		if err := linker.Connect(); err != nil {
			log.Fatalf("Failed to connect to server: %v", err)
		}
		defer linker.Close()

		// Look for the "--" separator which indicates the start of the linker command
		linkerCmd := []string{}
		separatorFound := false
		for i, arg := range os.Args {
			if arg == "--" && i+1 < len(os.Args) {
				// We found the separator, pass all remaining args excluding the "--" separator
				linkerCmd = os.Args[i+1:] 
				separatorFound = true
				break
			}
		}

		if !separatorFound || len(linkerCmd) < 1 {
			log.Fatal("No linking command provided. Use -- to separate distcc-go options from the linker command")
		}

		if err := linker.HandleLink(linkerCmd); err != nil {
			log.Fatalf("Linking failed: %v", err)
		}
	} else if *serverMode {
		// Server mode - coordinates build jobs
		grpcServer, err := server.StartServer(*serverAddr)
		if err != nil {
			log.Fatalf("Failed to start server: %v", err)
		}

		log.Printf("Build server started on %s", *serverAddr)

		// Wait for shutdown signal
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan

		log.Println("Shutting down server...")
		grpcServer.GracefulStop()
	} else if *workerMode {
		// Worker mode - processes build jobs
		w := worker.NewWorker(*serverAddr, *fsMount, int32(*maxJobs))

		// Set linking capabilities based on flag
		if *disableLinking {
			w.SetSupportsLinking(false)
		}

		if err := w.Connect(); err != nil {
			log.Fatalf("Failed to connect to server: %v", err)
		}

		log.Printf("Worker started with %d max jobs, connecting to %s", *maxJobs, *serverAddr)

		// Create a context that we can cancel
		ctx, cancel := context.WithCancel(context.Background())

		// Set up signal handling
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

		// Start processing in a goroutine
		errorChan := make(chan error, 1)
		go func() {
			errorChan <- w.Start(ctx)
		}()

		// Wait for signal or error
		select {
		case <-sigChan:
			log.Println("Shutdown signal received")
			cancel()
		case err := <-errorChan:
			log.Printf("Worker error: %v", err)
		}

		// Clean shutdown
		if err := w.Close(); err != nil {
			log.Printf("Error during worker shutdown: %v", err)
		}
	}
}
