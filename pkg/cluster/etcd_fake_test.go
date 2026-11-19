/* Copyright 2026 AtomAI
*
* Licensed under the Apache License, Version 2.0 (the "License");
* you may not use this file except in compliance with the License.
* You may obtain a copy of the License at
*
*     http://www.apache.org/licenses/LICENSE-2.0
*
* Unless required by applicable law or agreed to in writing, software
* distributed under the License is distributed on an "AS IS" BASIS,
* WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
* See the License for the specific language governing permissions and
* limitations under the License.
 */

package cluster

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	pb "go.etcd.io/etcd/api/v3/etcdserverpb"
	"go.etcd.io/etcd/api/v3/mvccpb"
	"go.etcd.io/etcd/api/v3/v3rpc/rpctypes"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/vcaesar/zedsearch/pkg/errors"
)

// errInjected is returned by every fake etcd call while fail is set. It is
// not Unavailable, so the etcd client gives up instead of retrying.
var errInjected = status.Error(codes.FailedPrecondition, "injected failure")

// fakeEtcd is a gRPC server speaking just enough of the etcd v3 API for
// EtcdStore, backed by a MemoryStore. It lets the real etcd client run in
// tests without an etcd process.
type fakeEtcd struct {
	pb.UnimplementedKVServer
	pb.UnimplementedLeaseServer
	pb.UnimplementedWatchServer

	mem  *MemoryStore
	addr string
	fail atomic.Bool
}

// startFakeEtcd serves a fake etcd on a loopback port until the test ends.
func startFakeEtcd(t *testing.T) *fakeEtcd {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	fake := &fakeEtcd{mem: NewMemoryStore(), addr: lis.Addr().String()}
	server := grpc.NewServer()
	pb.RegisterKVServer(server, fake)
	pb.RegisterLeaseServer(server, fake)
	pb.RegisterWatchServer(server, fake)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := server.Serve(lis); err != nil {
			t.Logf("fake etcd: %v", err)
		}
	}()
	t.Cleanup(func() {
		server.Stop()
		<-done
		require.NoError(t, fake.mem.Close())
	})
	return fake
}

func (f *fakeEtcd) check() error {
	if f.fail.Load() {
		return errInjected
	}
	return nil
}

func header(rev int64) *pb.ResponseHeader {
	return &pb.ResponseHeader{Revision: rev}
}

func toPB(kv KV) *mvccpb.KeyValue {
	return &mvccpb.KeyValue{Key: []byte(kv.Key), Value: kv.Value, ModRevision: kv.Revision}
}

func (f *fakeEtcd) Range(ctx context.Context, req *pb.RangeRequest) (*pb.RangeResponse, error) {
	if err := f.check(); err != nil {
		return nil, err
	}
	resp := &pb.RangeResponse{Header: header(0)}
	if len(req.RangeEnd) == 0 {
		kv, err := f.mem.Get(ctx, string(req.Key))
		if errors.Is(err, errors.ErrKeyNotFound) {
			return resp, nil
		}
		if err != nil {
			return nil, err
		}
		resp.Kvs = []*mvccpb.KeyValue{toPB(kv)}
		return resp, nil
	}
	kvs, err := f.mem.List(ctx, string(req.Key))
	if err != nil {
		return nil, err
	}
	for _, kv := range kvs {
		resp.Kvs = append(resp.Kvs, toPB(kv))
	}
	resp.Count = int64(len(resp.Kvs))
	return resp, nil
}

func (f *fakeEtcd) Put(ctx context.Context, req *pb.PutRequest) (*pb.PutResponse, error) {
	if err := f.check(); err != nil {
		return nil, err
	}
	rev, err := f.mem.Put(ctx, string(req.Key), req.Value, LeaseID(req.Lease))
	if err != nil {
		return nil, err
	}
	return &pb.PutResponse{Header: header(rev)}, nil
}

func (f *fakeEtcd) DeleteRange(ctx context.Context, req *pb.DeleteRangeRequest) (*pb.DeleteRangeResponse, error) {
	if err := f.check(); err != nil {
		return nil, err
	}
	if err := f.mem.Delete(ctx, string(req.Key)); err != nil {
		return nil, err
	}
	return &pb.DeleteRangeResponse{Header: header(0)}, nil
}

// Txn supports the two shapes EtcdStore sends: create-if-absent and
// compare-and-swap on the mod revision, each with a put and an else-get.
func (f *fakeEtcd) Txn(ctx context.Context, req *pb.TxnRequest) (*pb.TxnResponse, error) {
	if err := f.check(); err != nil {
		return nil, err
	}
	cmp, put := req.Compare[0], req.Success[0].GetRequestPut()
	key, lease := string(cmp.Key), LeaseID(put.Lease)

	var (
		rev int64
		ok  bool
		err error
	)
	if cmp.Target == pb.Compare_CREATE {
		rev, ok, err = f.mem.Create(ctx, key, put.Value, lease)
	} else {
		rev, ok, err = f.mem.CompareAndSwap(ctx, key, put.Value, cmp.GetModRevision(), lease)
	}
	if err != nil {
		return nil, err
	}
	if ok {
		return &pb.TxnResponse{
			Header:    header(rev),
			Succeeded: true,
			Responses: []*pb.ResponseOp{{Response: &pb.ResponseOp_ResponsePut{ResponsePut: &pb.PutResponse{Header: header(rev)}}}},
		}, nil
	}
	get := &pb.RangeResponse{Header: header(0)}
	if rev != 0 {
		get.Kvs = []*mvccpb.KeyValue{{Key: cmp.Key, ModRevision: rev}}
	}
	return &pb.TxnResponse{
		Header:    header(0),
		Responses: []*pb.ResponseOp{{Response: &pb.ResponseOp_ResponseRange{ResponseRange: get}}},
	}, nil
}

func (f *fakeEtcd) LeaseGrant(ctx context.Context, req *pb.LeaseGrantRequest) (*pb.LeaseGrantResponse, error) {
	if err := f.check(); err != nil {
		return nil, err
	}
	id, err := f.mem.Grant(ctx, time.Duration(req.TTL)*time.Second)
	if err != nil {
		return nil, err
	}
	return &pb.LeaseGrantResponse{Header: header(0), ID: int64(id), TTL: req.TTL}, nil
}

func (f *fakeEtcd) LeaseRevoke(ctx context.Context, req *pb.LeaseRevokeRequest) (*pb.LeaseRevokeResponse, error) {
	if err := f.check(); err != nil {
		return nil, err
	}
	// MemoryStore ignores unknown leases, etcd reports them
	if err := f.mem.KeepAlive(ctx, LeaseID(req.ID)); err != nil {
		return nil, rpctypes.ErrGRPCLeaseNotFound
	}
	if err := f.mem.Revoke(ctx, LeaseID(req.ID)); err != nil {
		return nil, err
	}
	return &pb.LeaseRevokeResponse{Header: header(0)}, nil
}

func (f *fakeEtcd) LeaseKeepAlive(stream pb.Lease_LeaseKeepAliveServer) error {
	for {
		req, err := stream.Recv()
		if err != nil {
			return nil // client closed the stream
		}
		if err := f.check(); err != nil {
			return err
		}
		resp := &pb.LeaseKeepAliveResponse{Header: header(0), ID: req.ID, TTL: 1}
		if err := f.mem.KeepAlive(stream.Context(), LeaseID(req.ID)); err != nil {
			resp.TTL = 0 // etcd reports an unknown lease with a zero ttl
		}
		if err := stream.Send(resp); err != nil {
			return err
		}
	}
}

// Watch serves every create request on the stream from a MemoryStore watch,
// the key is used as the prefix.
func (f *fakeEtcd) Watch(stream pb.Watch_WatchServer) error {
	var (
		sendMu sync.Mutex
		nextID int64
	)
	send := func(resp *pb.WatchResponse) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return stream.Send(resp)
	}
	for {
		req, err := stream.Recv()
		if err != nil {
			return nil // client closed the stream
		}
		create := req.GetCreateRequest()
		if create == nil {
			continue // cancel and progress requests need no answer here
		}
		if err := f.check(); err != nil {
			return err
		}
		events, err := f.mem.Watch(stream.Context(), string(create.Key))
		if err != nil {
			return err
		}
		id := nextID
		nextID++
		if err := send(&pb.WatchResponse{Header: header(0), WatchId: id, Created: true}); err != nil {
			return err
		}
		go func() {
			for event := range events {
				ev := &mvccpb.Event{Type: mvccpb.PUT, Kv: toPB(event.KV)}
				if event.Type == EventDelete {
					ev.Type = mvccpb.DELETE
				}
				if send(&pb.WatchResponse{Header: header(event.KV.Revision), WatchId: id, Events: []*mvccpb.Event{ev}}) != nil {
					return
				}
			}
		}()
	}
}
