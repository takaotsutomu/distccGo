# distccGo

distccGo is a distributed build system that works with a shared filesystem (e.g., CephFS) to accelerate C/C++ builds by distributing compilation and linking jobs across multiple nodes.

## Architecture

The system consists of four main components:

1. **Compiler Launcher** - A compiler wrapper that intercepts compilation commands and sends them to the server
2. **Linker Launcher** - A linker wrapper that intercepts linking commands and sends them to the server
3. **Server** - Coordinates job distribution and manages workers
4. **Worker** - Processes compilation and linking jobs using the shared filesystem

All components communicate using gRPC and share files through a shared filesystem mount point.

## Requirements

- Go 1.20 or higher
- Shared file system mounted on all nodes at the same path
- gRPC and Protocol Buffers

## Installation

```bash
# Install dependencies
go mod tidy

# Generate gRPC code
protoc --go_out=module=github.com/takaotsutomu/distccGo:. --go-grpc_out=module=github.com/takaotsutomu/distccGo:. internal/proto/builder.proto

# Build the binary
go build -o bin/distcc-go cmd/distcc/main.go
```

## Usage

### Starting the Server

Start the central coordination server:

```bash
./bin/distcc-go --server --server-addr=server-hostname:50051
```

### Starting Workers

Start worker nodes to process compilation and linking jobs:

```bash
./bin/distcc-go --worker --server-addr=server-hostname:50051 --fs-mount=/mnt/cephfs --max-jobs=8
```

To start a worker that only processes compilation jobs (no linking):

```bash
./bin/distcc-go --worker --server-addr=server-hostname:50051 --fs-mount=/mnt/cephfs --max-jobs=8 --disable-linking
```

To configure worker directory tracking for locality-aware scheduling:

```bash
./bin/distcc-go --worker --server-addr=server-hostname:50051 --fs-mount=/mnt/cephfs --max-jobs=8 \
  --max-dirs-tracked=200 --dir-report-interval=3m
```

### Using the Compiler Launcher

Use the launcher as a compiler wrapper:

```bash
./bin/distcc-go --compiler --server-addr=server-hostname:50051 --fs-mount=/mnt/cephfs -- g++ -c main.cpp -o main.o
```

With job batching enabled:

```bash
./bin/distcc-go --compiler --server-addr=server-hostname:50051 --fs-mount=/mnt/cephfs \
  --batch-size=20 --batch-timeout=500ms -- g++ -c main.cpp -o main.o
```

### Using the Linker Launcher

Use the launcher as a linker wrapper:

```bash
./bin/distcc-go --linker --server-addr=server-hostname:50051 --fs-mount=/mnt/cephfs -- g++ -o myapp main.o utils.o -lm
```

With job batching enabled:

```bash
./bin/distcc-go --linker --server-addr=server-hostname:50051 --fs-mount=/mnt/cephfs \
  --batch-size=5 --batch-timeout=1s -- g++ -o myapp main.o utils.o -lm
```

### Command-Line Options

#### Common Options

- `--server-addr=STRING`: Build server address (default: "localhost:50051")
- `--fs-mount=STRING`: Path to the shared filesystem mount point (default: "/mnt/cephfs")
- `--build-dir=STRING`: Path to the build directory (root of build outputs)

#### Worker Options

- `--max-jobs=INT`: Maximum parallel jobs for worker (default: 4)
- `--disable-linking`: Disable linking support for this worker
- `--max-dirs-tracked=INT`: Maximum number of directories to track per worker (default: 100)
- `--dir-report-interval=DURATION`: Interval for reporting directory statistics (default: 5m)

#### Compiler/Linker Options

- `--batch-size=INT`: Number of jobs to batch together (default: 1, no batching)
- `--batch-timeout=DURATION`: Maximum time to wait for a batch to fill (default: 500ms)

### Integration with CMake

Add this to your CMake configuration to use distccGo:

```cmake
# For compilation
set(CMAKE_CXX_COMPILER_LAUNCHER "/path/to/bin/distcc-go" "--compiler" "--server-addr=server-hostname:50051" "--fs-mount=/mnt/cephfs" 
"--build-dir=${BUILD_DIR}" "--batch-size=20" "--batch-timeout=500ms" "--")

# For linking (CMake 3.21+)
set(CMAKE_CXX_LINKER_LAUNCHER "/path/to/bin/distcc-go" "--linker" "--server-addr=server-hostname:50051" "--fs-mount=/mnt/cephfs" 
"--build-dir=${BUILD_DIR}" "--batch-size=5" "--batch-timeout=1s" "--")
```

## How It Works

distccGo follows a MapReduce-like pattern:

1. **Map Phase (Compilation)**:
   - The compiler launcher intercepts compilation commands
   - The server distributes compilation jobs to available workers
   - Workers compile source files to object files in parallel
   - Results are stored in the shared filesystem

2. **Reduce Phase (Linking)**:
   - The linker launcher intercepts linking commands
   - The server assigns linking jobs to workers with sufficient resources
   - Workers link object files into binaries
   - Final outputs are stored in the shared filesystem

### Fault Tolerance

The system is designed to be fault-tolerant:

- If a worker fails during a job, the server detects the failure and reassigns the job
- Workers automatically reconnect if they lose connection to the server
- Both the server and workers maintain job tracking for recovery

## Advantages

- **Scalability**: Easy to add more worker nodes without configuration changes
- **Reliability**: Jobs are automatically reassigned if a worker fails
- **Completeness**: Distributes both compilation and linking, maximizing parallelism
- **Performance**: Job batching and locality-aware scheduling improve throughput and efficiency
