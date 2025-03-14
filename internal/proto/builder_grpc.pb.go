// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// versions:
// - protoc-gen-go-grpc v1.5.1
// - protoc             v5.29.3
// source: internal/proto/builder.proto

package proto

import (
	context "context"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.64.0 or later.
const _ = grpc.SupportPackageIsVersion9

const (
	BuildService_SubmitCompileJob_FullMethodName = "/builder.BuildService/SubmitCompileJob"
	BuildService_SubmitLinkJob_FullMethodName    = "/builder.BuildService/SubmitLinkJob"
	BuildService_WorkerStream_FullMethodName     = "/builder.BuildService/WorkerStream"
	BuildService_ReportJobStatus_FullMethodName  = "/builder.BuildService/ReportJobStatus"
	BuildService_GetJobStatus_FullMethodName     = "/builder.BuildService/GetJobStatus"
)

// BuildServiceClient is the client API for BuildService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type BuildServiceClient interface {
	// Submit a compilation job
	SubmitCompileJob(ctx context.Context, in *CompileJobRequest, opts ...grpc.CallOption) (*CompileJobResponse, error)
	// Submit a linking job
	SubmitLinkJob(ctx context.Context, in *LinkJobRequest, opts ...grpc.CallOption) (*LinkJobResponse, error)
	// Stream for workers to receive jobs
	WorkerStream(ctx context.Context, in *WorkerRegistration, opts ...grpc.CallOption) (grpc.ServerStreamingClient[JobRequest], error)
	// Report job completion status
	ReportJobStatus(ctx context.Context, in *JobStatusReport, opts ...grpc.CallOption) (*JobStatusAck, error)
	// Get the status of a job
	GetJobStatus(ctx context.Context, in *JobStatusRequest, opts ...grpc.CallOption) (*JobStatusResponse, error)
}

type buildServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewBuildServiceClient(cc grpc.ClientConnInterface) BuildServiceClient {
	return &buildServiceClient{cc}
}

func (c *buildServiceClient) SubmitCompileJob(ctx context.Context, in *CompileJobRequest, opts ...grpc.CallOption) (*CompileJobResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(CompileJobResponse)
	err := c.cc.Invoke(ctx, BuildService_SubmitCompileJob_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *buildServiceClient) SubmitLinkJob(ctx context.Context, in *LinkJobRequest, opts ...grpc.CallOption) (*LinkJobResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(LinkJobResponse)
	err := c.cc.Invoke(ctx, BuildService_SubmitLinkJob_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *buildServiceClient) WorkerStream(ctx context.Context, in *WorkerRegistration, opts ...grpc.CallOption) (grpc.ServerStreamingClient[JobRequest], error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	stream, err := c.cc.NewStream(ctx, &BuildService_ServiceDesc.Streams[0], BuildService_WorkerStream_FullMethodName, cOpts...)
	if err != nil {
		return nil, err
	}
	x := &grpc.GenericClientStream[WorkerRegistration, JobRequest]{ClientStream: stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

// This type alias is provided for backwards compatibility with existing code that references the prior non-generic stream type by name.
type BuildService_WorkerStreamClient = grpc.ServerStreamingClient[JobRequest]

func (c *buildServiceClient) ReportJobStatus(ctx context.Context, in *JobStatusReport, opts ...grpc.CallOption) (*JobStatusAck, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(JobStatusAck)
	err := c.cc.Invoke(ctx, BuildService_ReportJobStatus_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *buildServiceClient) GetJobStatus(ctx context.Context, in *JobStatusRequest, opts ...grpc.CallOption) (*JobStatusResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(JobStatusResponse)
	err := c.cc.Invoke(ctx, BuildService_GetJobStatus_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// BuildServiceServer is the server API for BuildService service.
// All implementations must embed UnimplementedBuildServiceServer
// for forward compatibility.
type BuildServiceServer interface {
	// Submit a compilation job
	SubmitCompileJob(context.Context, *CompileJobRequest) (*CompileJobResponse, error)
	// Submit a linking job
	SubmitLinkJob(context.Context, *LinkJobRequest) (*LinkJobResponse, error)
	// Stream for workers to receive jobs
	WorkerStream(*WorkerRegistration, grpc.ServerStreamingServer[JobRequest]) error
	// Report job completion status
	ReportJobStatus(context.Context, *JobStatusReport) (*JobStatusAck, error)
	// Get the status of a job
	GetJobStatus(context.Context, *JobStatusRequest) (*JobStatusResponse, error)
	mustEmbedUnimplementedBuildServiceServer()
}

// UnimplementedBuildServiceServer must be embedded to have
// forward compatible implementations.
//
// NOTE: this should be embedded by value instead of pointer to avoid a nil
// pointer dereference when methods are called.
type UnimplementedBuildServiceServer struct{}

func (UnimplementedBuildServiceServer) SubmitCompileJob(context.Context, *CompileJobRequest) (*CompileJobResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method SubmitCompileJob not implemented")
}
func (UnimplementedBuildServiceServer) SubmitLinkJob(context.Context, *LinkJobRequest) (*LinkJobResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method SubmitLinkJob not implemented")
}
func (UnimplementedBuildServiceServer) WorkerStream(*WorkerRegistration, grpc.ServerStreamingServer[JobRequest]) error {
	return status.Errorf(codes.Unimplemented, "method WorkerStream not implemented")
}
func (UnimplementedBuildServiceServer) ReportJobStatus(context.Context, *JobStatusReport) (*JobStatusAck, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ReportJobStatus not implemented")
}
func (UnimplementedBuildServiceServer) GetJobStatus(context.Context, *JobStatusRequest) (*JobStatusResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetJobStatus not implemented")
}
func (UnimplementedBuildServiceServer) mustEmbedUnimplementedBuildServiceServer() {}
func (UnimplementedBuildServiceServer) testEmbeddedByValue()                      {}

// UnsafeBuildServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to BuildServiceServer will
// result in compilation errors.
type UnsafeBuildServiceServer interface {
	mustEmbedUnimplementedBuildServiceServer()
}

func RegisterBuildServiceServer(s grpc.ServiceRegistrar, srv BuildServiceServer) {
	// If the following call pancis, it indicates UnimplementedBuildServiceServer was
	// embedded by pointer and is nil.  This will cause panics if an
	// unimplemented method is ever invoked, so we test this at initialization
	// time to prevent it from happening at runtime later due to I/O.
	if t, ok := srv.(interface{ testEmbeddedByValue() }); ok {
		t.testEmbeddedByValue()
	}
	s.RegisterService(&BuildService_ServiceDesc, srv)
}

func _BuildService_SubmitCompileJob_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(CompileJobRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(BuildServiceServer).SubmitCompileJob(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: BuildService_SubmitCompileJob_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(BuildServiceServer).SubmitCompileJob(ctx, req.(*CompileJobRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _BuildService_SubmitLinkJob_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(LinkJobRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(BuildServiceServer).SubmitLinkJob(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: BuildService_SubmitLinkJob_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(BuildServiceServer).SubmitLinkJob(ctx, req.(*LinkJobRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _BuildService_WorkerStream_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(WorkerRegistration)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(BuildServiceServer).WorkerStream(m, &grpc.GenericServerStream[WorkerRegistration, JobRequest]{ServerStream: stream})
}

// This type alias is provided for backwards compatibility with existing code that references the prior non-generic stream type by name.
type BuildService_WorkerStreamServer = grpc.ServerStreamingServer[JobRequest]

func _BuildService_ReportJobStatus_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(JobStatusReport)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(BuildServiceServer).ReportJobStatus(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: BuildService_ReportJobStatus_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(BuildServiceServer).ReportJobStatus(ctx, req.(*JobStatusReport))
	}
	return interceptor(ctx, in, info, handler)
}

func _BuildService_GetJobStatus_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(JobStatusRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(BuildServiceServer).GetJobStatus(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: BuildService_GetJobStatus_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(BuildServiceServer).GetJobStatus(ctx, req.(*JobStatusRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// BuildService_ServiceDesc is the grpc.ServiceDesc for BuildService service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var BuildService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "builder.BuildService",
	HandlerType: (*BuildServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "SubmitCompileJob",
			Handler:    _BuildService_SubmitCompileJob_Handler,
		},
		{
			MethodName: "SubmitLinkJob",
			Handler:    _BuildService_SubmitLinkJob_Handler,
		},
		{
			MethodName: "ReportJobStatus",
			Handler:    _BuildService_ReportJobStatus_Handler,
		},
		{
			MethodName: "GetJobStatus",
			Handler:    _BuildService_GetJobStatus_Handler,
		},
	},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "WorkerStream",
			Handler:       _BuildService_WorkerStream_Handler,
			ServerStreams: true,
		},
	},
	Metadata: "internal/proto/builder.proto",
}
