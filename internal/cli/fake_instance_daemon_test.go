// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strconv"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/konradasb/dicer"
	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// fakeInstanceDaemon keeps instances in memory and moves them between states
// as the daemon would, recording the requests that change them.
type fakeInstanceDaemon struct {
	dicerdv1.UnimplementedDaemonServiceServer

	mu        sync.Mutex
	instances map[string]*dicerdv1.Instance
	calls     []string
	created   *dicerdv1.CreateInstanceRequest
	updated   *dicerdv1.UpdateInstanceRequest
	resized   *dicerdv1.ResizeInstanceRequest

	// cached are the images already on the host: pulling one of them
	// downloads nothing.
	cached map[string]bool
	// digest is the digest every image reference resolves to, which a new
	// instance is pinned to.
	digest string
	// kernels and networks are what the host has, and host what
	// GetHostInfo says of it.
	kernels  []string
	networks []string
	host     *dicerdv1.GetHostInfoResponse
	// hostDelay holds up GetHostInfo, to trip --request-timeout.
	hostDelay time.Duration

	// events is what GetEvents streams once it has caught up. A test that
	// waits on an instance pushes what happens to it here.
	events chan *dicerdv1.Event
	// history is what GetEvents streams before it has caught up: what
	// happened before the command asked.
	history []*dicerdv1.Event
	// waiters hear each instance's next stop, by instance ID.
	waiters map[string][]chan *dicerdv1.WaitInstanceResponse

	// console is what each instance's console holds, and ran which
	// instances have been started, and so have one.
	console map[string]string
	ran     map[string]bool
	// onStart, if set, is what happens as an instance starts.
	onStart func(name string)
}

func newFakeInstanceDaemon(instances ...*dicerdv1.Instance) *fakeInstanceDaemon {
	d := &fakeInstanceDaemon{
		instances: make(map[string]*dicerdv1.Instance),
		cached:    make(map[string]bool),
		digest:    "sha256:0123456789abcdef0123",
		host:      &dicerdv1.GetHostInfoResponse{Version: "v9.9.9", Hostname: "compute-1"},
		events:    make(chan *dicerdv1.Event, 8),
		waiters:   make(map[string][]chan *dicerdv1.WaitInstanceResponse),
		console:   make(map[string]string),
		ran:       make(map[string]bool),
	}
	for _, instance := range instances {
		d.instances[instance.GetName()] = instance
	}
	return d
}

func (d *fakeInstanceDaemon) record(call string) {
	d.calls = append(d.calls, call)
}

func (d *fakeInstanceDaemon) get(name string) (*dicerdv1.Instance, error) {
	instance, ok := d.instances[name]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "no instance %q", name)
	}
	return instance, nil
}

// reply copies an instance for the wire. gRPC marshals what a handler
// returns after the handler has returned, so the lock is long released by
// then; handing out the stored message would let a test that moves an
// instance on -- stops, say -- write it while it is being marshalled.
func reply(instance *dicerdv1.Instance, err error) (*dicerdv1.Instance, error) {
	if err != nil {
		return nil, err
	}

	clone, ok := proto.Clone(instance).(*dicerdv1.Instance)
	if !ok {
		return nil, errors.New("clone instance")
	}

	return clone, nil
}

// setState moves an instance to state if it is in one of from.
func (d *fakeInstanceDaemon) setState(
	call, name string, state dicerdv1.InstanceState, from ...dicerdv1.InstanceState,
) (*dicerdv1.Instance, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	instance, err := d.get(name)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(from, instance.GetState()) {
		return nil, status.Errorf(codes.FailedPrecondition, "instance %q is %s", name, stateWord(instance.GetState()))
	}

	d.record(call + " " + name)
	instance.State = state
	return reply(instance, nil)
}

func (d *fakeInstanceDaemon) ListInstances(
	context.Context, *dicerdv1.ListInstancesRequest,
) (*dicerdv1.ListInstancesResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	resp := &dicerdv1.ListInstancesResponse{}
	for _, name := range slices.Sorted(maps.Keys(d.instances)) {
		instance, err := reply(d.instances[name], nil)
		if err != nil {
			return nil, err
		}
		resp.Instances = append(resp.Instances, instance)
	}
	return resp, nil
}

func (d *fakeInstanceDaemon) GetInstance(_ context.Context, req *dicerdv1.GetInstanceRequest) (*dicerdv1.Instance, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	return reply(d.get(req.GetName()))
}

func (d *fakeInstanceDaemon) StartInstance(
	_ context.Context, req *dicerdv1.StartInstanceRequest,
) (*dicerdv1.Instance, error) {
	instance, err := d.setState("start", req.GetName(), stateRunning, stateStopped, stateFailed)
	if err != nil {
		return nil, err
	}

	d.mu.Lock()
	d.ran[req.GetName()] = true
	onStart := d.onStart
	d.mu.Unlock()

	if onStart != nil {
		onStart(req.GetName())
	}
	return instance, nil
}

func (d *fakeInstanceDaemon) StopInstance(_ context.Context, req *dicerdv1.StopInstanceRequest) (*dicerdv1.Instance, error) {
	instance, err := d.setState("stop", req.GetName(), stateStopped, stateRunning, statePaused)
	if err != nil {
		return nil, err
	}
	d.notifyWaiters(instance.GetId(), &dicerdv1.WaitInstanceResponse{State: stateStopped})
	return instance, nil
}

func (d *fakeInstanceDaemon) PauseInstance(
	_ context.Context, req *dicerdv1.PauseInstanceRequest,
) (*dicerdv1.Instance, error) {
	return d.setState("pause", req.GetName(), statePaused, stateRunning)
}

func (d *fakeInstanceDaemon) ResumeInstance(
	_ context.Context, req *dicerdv1.ResumeInstanceRequest,
) (*dicerdv1.Instance, error) {
	return d.setState("resume", req.GetName(), stateRunning, statePaused)
}

func (d *fakeInstanceDaemon) DeleteInstance(
	_ context.Context, req *dicerdv1.DeleteInstanceRequest,
) (*emptypb.Empty, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	instance, err := d.get(req.GetName())
	if err != nil {
		return nil, err
	}
	if instance.GetState() == stateRunning && !req.GetForce() {
		return nil, status.Errorf(codes.FailedPrecondition, "instance %q is running", req.GetName())
	}
	d.record("delete " + req.GetName())
	delete(d.instances, req.GetName())
	return &emptypb.Empty{}, nil
}

func (d *fakeInstanceDaemon) CreateInstance(
	_ context.Context, req *dicerdv1.CreateInstanceRequest,
) (*dicerdv1.Instance, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.created = req
	instance := &dicerdv1.Instance{
		Id: "id-" + req.GetName(), Name: req.GetName(), ImageRef: req.GetImageRef(), ImageDigest: d.digest,
		State: stateStopped,
	}
	if req.GetStart() {
		instance.State, instance.Ip = stateRunning, "10.0.0.9"
	}
	d.instances[instance.GetName()] = instance
	return reply(instance, nil)
}

func (d *fakeInstanceDaemon) UpdateInstance(
	_ context.Context, req *dicerdv1.UpdateInstanceRequest,
) (*dicerdv1.Instance, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.updated = req
	return reply(d.get(req.GetName()))
}

// ResizeInstance gives an instance the sizes asked for.
func (d *fakeInstanceDaemon) ResizeInstance(
	_ context.Context, req *dicerdv1.ResizeInstanceRequest,
) (*dicerdv1.Instance, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.resized = req
	instance, err := reply(d.get(req.GetName()))
	if err != nil {
		return nil, err
	}
	if req.Vcpus != nil {
		instance.Vcpus = req.GetVcpus()
	}
	if req.MemoryBytes != nil {
		instance.MemoryBytes = req.GetMemoryBytes()
	}
	return instance, nil
}

// RenameInstance moves an instance to a new name, as the daemon does for a
// stopped one.
func (d *fakeInstanceDaemon) RenameInstance(
	_ context.Context, req *dicerdv1.RenameInstanceRequest,
) (*dicerdv1.Instance, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	instance, err := d.get(req.GetName())
	if err != nil {
		return nil, err
	}
	if instance.GetState() != stateStopped {
		return nil, status.Errorf(codes.FailedPrecondition,
			"instance %q is %s", req.GetName(), stateWord(instance.GetState()))
	}
	if _, taken := d.instances[req.GetNewName()]; taken {
		return nil, status.Errorf(codes.AlreadyExists, "instance %q already exists", req.GetNewName())
	}

	d.record("rename " + req.GetName() + " " + req.GetNewName())
	delete(d.instances, instance.GetName())
	instance.Name = req.GetNewName()
	d.instances[instance.GetName()] = instance

	return reply(instance, nil)
}

// GetEvents reports the history, says so, and then streams whatever the test
// pushes, which is the shape the daemon's own stream has.
func (d *fakeInstanceDaemon) GetEvents(
	req *dicerdv1.GetEventsRequest, stream grpc.ServerStreamingServer[dicerdv1.GetEventsResponse],
) error {
	d.mu.Lock()
	history := slices.Clone(d.history)
	d.mu.Unlock()

	if err := stream.Send(&dicerdv1.GetEventsResponse{Events: history, CaughtUp: true}); err != nil {
		return err
	}
	if !req.GetFollow() {
		return nil
	}

	for {
		select {
		case e := <-d.events:
			if err := stream.Send(&dicerdv1.GetEventsResponse{Events: []*dicerdv1.Event{e}}); err != nil {
				return err
			}
		case <-stream.Context().Done():
			return nil
		}
	}
}

// WaitInstance waits as the daemon does: it returns a stopped instance's
// status at once, unless asked for the next stop, and otherwise the next
// stop the test reports.
func (d *fakeInstanceDaemon) WaitInstance(
	req *dicerdv1.WaitInstanceRequest, stream grpc.ServerStreamingServer[dicerdv1.WaitInstanceResponse],
) error {
	d.mu.Lock()
	instance, err := d.get(req.GetName())
	if err != nil {
		d.mu.Unlock()
		return err
	}
	stopped := make(chan *dicerdv1.WaitInstanceResponse, 1)
	d.waiters[instance.GetId()] = append(d.waiters[instance.GetId()], stopped)
	now := &dicerdv1.WaitInstanceResponse{State: instance.GetState(), ExitCode: instance.ExitCode}
	d.mu.Unlock()

	if err := stream.SendHeader(nil); err != nil {
		return err
	}
	if !req.GetNextStop() && (now.GetState() == stateStopped || now.GetState() == stateFailed) {
		return stream.Send(now)
	}
	select {
	case resp := <-stopped:
		return stream.Send(resp)
	case <-stream.Context().Done():
		return stream.Context().Err()
	}
}

// notifyWaiters tells those waiting on an instance how it stopped.
func (d *fakeInstanceDaemon) notifyWaiters(id string, resp *dicerdv1.WaitInstanceResponse) {
	d.mu.Lock()
	defer d.mu.Unlock()

	for _, stopped := range d.waiters[id] {
		stopped <- resp
	}
	delete(d.waiters, id)
}

// stops records an instance as stopped with the given status, and reports it
// as the daemon would.
func (d *fakeInstanceDaemon) stops(name string, exitCode int32) {
	d.mu.Lock()
	id := "id-" + name
	if instance, ok := d.instances[name]; ok {
		instance.State = stateStopped
		instance.ExitCode = &exitCode
		id = instance.GetId()
	}
	d.mu.Unlock()

	d.notifyWaiters(id, &dicerdv1.WaitInstanceResponse{State: stateStopped, ExitCode: &exitCode})
	d.emit(exitedEvent(id, name, exitCode))
}

// stopsAndRemoves reports an instance as ended and deletes it, which is what
// the daemon does for one started with --rm.
func (d *fakeInstanceDaemon) stopsAndRemoves(name string, exitCode int32) {
	d.mu.Lock()
	id := "id-" + name
	if instance, ok := d.instances[name]; ok {
		id = instance.GetId()
	}
	delete(d.instances, name)
	d.mu.Unlock()

	d.notifyWaiters(id, &dicerdv1.WaitInstanceResponse{State: stateStopped, ExitCode: &exitCode})
	d.emit(exitedEvent(id, name, exitCode))
	d.emit(&dicerdv1.Event{
		Kind: dicerdv1.EventKind_EVENT_KIND_INSTANCE, Id: id, Name: name,
		Action: dicerdv1.EventAction_EVENT_ACTION_DELETED,
	})
}

// emit reports an event: to the history, and to a command following.
func (d *fakeInstanceDaemon) emit(e *dicerdv1.Event) {
	d.mu.Lock()
	d.history = append(d.history, e)
	d.mu.Unlock()

	d.events <- e
}

// exitedEvent is the event the daemon reports for an instance that exited.
func exitedEvent(id, name string, exitCode int32) *dicerdv1.Event {
	return &dicerdv1.Event{
		Kind: dicerdv1.EventKind_EVENT_KIND_INSTANCE, Id: id, Name: name,
		Action:     dicerdv1.EventAction_EVENT_ACTION_EXITED,
		Attributes: map[string]string{"exit_code": strconv.Itoa(int(exitCode))},
	}
}

// GetInstanceLogs sends an instance's console, which it has once started,
// and with follow waits for it to stop, as the daemon does.
func (d *fakeInstanceDaemon) GetInstanceLogs(
	req *dicerdv1.GetInstanceLogsRequest, stream grpc.ServerStreamingServer[dicerdv1.InstanceLogChunk],
) error {
	d.mu.Lock()
	_, exists := d.instances[req.GetName()]
	ran, console := d.ran[req.GetName()], d.console[req.GetName()]
	d.mu.Unlock()

	if !exists || !ran {
		return status.Errorf(codes.NotFound, "instance %q has no guest log yet", req.GetName())
	}
	if err := stream.Send(&dicerdv1.InstanceLogChunk{Data: []byte(console)}); err != nil {
		return err
	}

	for req.GetFollow() {
		d.mu.Lock()
		instance, ok := d.instances[req.GetName()]
		running := ok && instance.GetState() == stateRunning
		d.mu.Unlock()
		if !running {
			return nil
		}

		select {
		case <-stream.Context().Done():
			return nil
		case <-time.After(10 * time.Millisecond):
		}
	}
	return nil
}

func (d *fakeInstanceDaemon) GetHostInfo(
	ctx context.Context, _ *dicerdv1.GetHostInfoRequest,
) (*dicerdv1.GetHostInfoResponse, error) {
	select {
	case <-time.After(d.hostDelay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return d.host, nil
}

// GetImage returns an image only if it is cached.
func (d *fakeInstanceDaemon) GetImage(_ context.Context, req *dicerdv1.GetImageRequest) (*dicerdv1.Image, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.cached[req.GetRef()] {
		return nil, status.Errorf(codes.NotFound, "no image %q", req.GetRef())
	}
	return &dicerdv1.Image{Name: req.GetRef(), Digest: d.digest, SizeBytes: 64 << 20}, nil
}

// PullImage reports a download for an image not yet cached, and caches it.
func (d *fakeInstanceDaemon) PullImage(
	req *dicerdv1.PullImageRequest, stream grpc.ServerStreamingServer[dicerdv1.PullImageProgress],
) error {
	d.mu.Lock()
	d.record("pull " + req.GetRef())
	cached := d.cached[req.GetRef()]
	d.cached[req.GetRef()] = true
	d.mu.Unlock()

	progress := []*dicerdv1.PullImageProgress{{Stage: dicerdv1.PullStage_PULL_STAGE_RESOLVING}}
	if !cached {
		progress = append(progress,
			&dicerdv1.PullImageProgress{Stage: dicerdv1.PullStage_PULL_STAGE_DOWNLOADING, TotalBytes: 100},
			&dicerdv1.PullImageProgress{
				Stage: dicerdv1.PullStage_PULL_STAGE_DOWNLOADING, DownloadedBytes: 100, TotalBytes: 100,
			},
		)
	}
	progress = append(progress, &dicerdv1.PullImageProgress{
		Image: &dicerdv1.Image{Name: req.GetRef(), Digest: d.digest, SizeBytes: 64 << 20},
	})

	for _, p := range progress {
		if err := stream.Send(p); err != nil {
			return err
		}
	}
	return nil
}

func (d *fakeInstanceDaemon) ListKernels(
	context.Context, *dicerdv1.ListKernelsRequest,
) (*dicerdv1.ListKernelsResponse, error) {
	resp := &dicerdv1.ListKernelsResponse{}
	for _, k := range d.kernels {
		resp.Kernels = append(resp.Kernels, &dicerdv1.Kernel{Name: k, Arch: dicerdv1.Architecture_ARCHITECTURE_X86_64})
	}
	return resp, nil
}

func (d *fakeInstanceDaemon) ListNetworks(
	context.Context, *dicerdv1.ListNetworksRequest,
) (*dicerdv1.ListNetworksResponse, error) {
	resp := &dicerdv1.ListNetworksResponse{}
	for _, n := range d.networks {
		resp.Networks = append(resp.Networks, &dicerdv1.Network{Name: n, Subnet: "172.20.0.0/16"})
	}
	return resp, nil
}

// GetResources answers with the same host testResources describes, written
// out as the daemon would send it. The fake is a daemon, so it speaks the
// wire format, which is what the CLI works with.
func (d *fakeInstanceDaemon) GetResources(
	context.Context, *dicerdv1.GetResourcesRequest,
) (*dicerdv1.GetResourcesResponse, error) {
	r := testResources()
	capacity := func(c dicer.ResourceCapacity) *dicerdv1.ResourceCapacity {
		return &dicerdv1.ResourceCapacity{
			Host: c.Host, Reserved: c.Reserved, Overcommit: c.Overcommit,
			Allocatable: c.Allocatable, Allocated: c.Allocated, Available: c.Available,
		}
	}
	return &dicerdv1.GetResourcesResponse{
		Cpu:    capacity(r.CPU),
		Memory: capacity(r.Memory),
		Disk: &dicerdv1.DiskUsage{
			Path: r.Disk.Path, TotalBytes: r.Disk.TotalBytes, FreeBytes: r.Disk.FreeBytes,
			ProvisionedBytes: r.Disk.ProvisionedBytes,
		},
	}, nil
}

// fakeInstances are what the tests start from: one of each state that
// matters, with labels and ports to filter and show.
func fakeInstances() []*dicerdv1.Instance {
	return []*dicerdv1.Instance{
		{
			Name: "web", ImageRef: "docker.io/library/nginx:1.27", State: stateRunning,
			NetworkName: "default", Ip: "10.0.0.5", Vcpus: 2, MemoryBytes: 1 << 30, DiskBytes: 10 << 30,
			Labels: map[string]string{"team": "web"},
			Env:    map[string]string{"B": "2", "A": "1"},
			Cmd:    []string{"nginx", "-g", "daemon off;"},
			Ports: []*dicerdv1.PortMapping{
				{HostPort: 8080, GuestPort: 80, Protocol: dicerdv1.Protocol_PROTOCOL_TCP},
				{HostIp: "10.1.0.1", HostPort: 5353, GuestPort: 53, Protocol: dicerdv1.Protocol_PROTOCOL_UDP},
			},
		},
		{
			Name: "db", ImageRef: "docker.io/library/postgres:17", State: stateStopped,
			NetworkName: "default", Labels: map[string]string{"team": "data"},
		},
		{Name: "cache", ImageRef: "docker.io/library/redis:7", State: statePaused, NetworkName: "backend"},
	}
}
