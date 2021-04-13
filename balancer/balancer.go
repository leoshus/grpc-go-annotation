/*
 *
 * Copyright 2017 gRPC authors.
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
 *
 */

// Package balancer defines APIs for load balancing in gRPC.
// All APIs in this package are experimental.
package balancer

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"

	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/internal"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/serviceconfig"
)

var (
	// m is a map from name to balancer builder.
	m = make(map[string]Builder)
)

// Register registers the balancer builder to the balancer map. b.Name
// (lowercased) will be used as the name registered with this builder.  If the
// Builder implements ConfigParser, ParseConfig will be called when new service
// configs are received by the resolver, and the result will be provided to the
// Balancer in UpdateClientConnState.
//
// NOTE: this function must only be called during initialization time (i.e. in
// an init() function), and is not thread-safe. If multiple Balancers are
// registered with the same name, the one registered last will take effect.
func Register(b Builder) {
	m[strings.ToLower(b.Name())] = b
}

// unregisterForTesting deletes the balancer with the given name from the
// balancer map.
//
// This function is not thread-safe.
func unregisterForTesting(name string) {
	delete(m, name)
}

func init() {
	internal.BalancerUnregister = unregisterForTesting
}

// Get returns the resolver builder registered with the given name.
// Note that the compare is done in a case-insensitive fashion.
// If no builder is register with the name, nil will be returned.
func Get(name string) Builder {
	if b, ok := m[strings.ToLower(name)]; ok {
		return b
	}
	return nil
}

// SubConn represents a gRPC sub connection.
// Each sub connection contains a list of addresses. gRPC will
// try to connect to them (in sequence), and stop trying the
// remainder once one connection is successful.
//
// The reconnect backoff will be applied on the list, not a single address.
// For example, try_on_all_addresses -> backoff -> try_on_all_addresses.
//
// All SubConns start in IDLE, and will not try to connect. To trigger
// the connecting, Balancers must call Connect.
// When the connection encounters an error, it will reconnect immediately.
// When the connection becomes IDLE, it will not reconnect unless Connect is
// called.
//
// This interface is to be implemented by gRPC. Users should not need a
// brand new implementation of this interface. For the situations like
// testing, the new implementation should embed this interface. This allows
// gRPC to add new methods to this interface.
// SubConn表示gRPC的sub connection
// 每个子连接包含一个地址列表。gRPC将尝试按顺序连接他们，一旦一个连接成功则终止剩余的其他连接。
// 重新连接退避将应用于列表，而不是单个地址。比如，try_on_all_addresses -> backoff -> try_on_all_addresses.
// 所有的SubConns开始状态为IDLE并且不会尝试连接。Balancers必须调用Connect来触发连接
// 当连接遇到错误，立即进行重连
// 当连接变成IDLE，不会进行连接除非调用Connect方法
// 该接口将由gRPC实现。 用户不需要该接口的全新实现。 对于类似测试的情况，新的实现应嵌入此接口。 这使gRPC可以向该接口添加新方法。
type SubConn interface {
	// UpdateAddresses updates the addresses used in this SubConn.
	// gRPC checks if currently-connected address is still in the new list.
	// If it's in the list, the connection will be kept.
	// If it's not in the list, the connection will gracefully closed, and
	// a new connection will be created.
	//
	// This will trigger a state transition for the SubConn.
	/// 将会触发SubConn的状态转移
	UpdateAddresses([]resolver.Address)
	// Connect starts the connecting for this SubConn.
	// 为此SubConn发起连接
	Connect()
}

// NewSubConnOptions contains options to create new SubConn.
type NewSubConnOptions struct {
	// CredsBundle is the credentials bundle that will be used in the created
	// SubConn. If it's nil, the original creds from grpc DialOptions will be
	// used.
	//
	// Deprecated: Use the Attributes field in resolver.Address to pass
	// arbitrary data to the credential handshaker.
	CredsBundle credentials.Bundle
	// HealthCheckEnabled indicates whether health check service should be
	// enabled on this SubConn
	HealthCheckEnabled bool
}

// State contains the balancer's state relevant to the gRPC ClientConn.
type State struct {
	// State contains the connectivity state of the balancer, which is used to
	// determine the state of the ClientConn.
	ConnectivityState connectivity.State
	// Picker is used to choose connections (SubConns) for RPCs.
	Picker Picker
}

// ClientConn 代表一个gRPC ClientConn
type ClientConn interface {
	// NewSubConn被balancer调用来创建一个新的SubConn
	// 不会阻塞等待连接的建立
	// SubConn的行为被options控制
	NewSubConn([]resolver.Address, NewSubConnOptions) (SubConn, error)
	// RemoveSubConn 将SubConn从ClientConn移除，此SubConn将被关闭
	RemoveSubConn(SubConn)
	// UpdateState 通知gRPC balancer的内部状态已经变更
	// gRPC将会更新ClientConn的连接状态，并将在新的Picker上调用Pick方法选择一个新的SubConn
	UpdateState(State)
	// ResolveNow 被balancer调用来通知gRPC进行名称解析
	ResolveNow(resolver.ResolveNowOptions)
	// Target 返回此ClientConn的拨号目标。
	// 已废弃: 使用 BuildOptions中的Target字段代替
	Target() string
}

// BuildOptions contains additional information for Build.
type BuildOptions struct {
	// DialCreds is the transport credential the Balancer implementation can
	// use to dial to a remote load balancer server. The Balancer implementations
	// can ignore this if it does not need to talk to another party securely.
	DialCreds credentials.TransportCredentials
	// CredsBundle is the credentials bundle that the Balancer can use.
	CredsBundle credentials.Bundle
	// Dialer is the custom dialer the Balancer implementation can use to dial
	// to a remote load balancer server. The Balancer implementations
	// can ignore this if it doesn't need to talk to remote balancer.
	Dialer func(context.Context, string) (net.Conn, error)
	// ChannelzParentID is the entity parent's channelz unique identification number.
	ChannelzParentID int64
	// CustomUserAgent is the custom user agent set on the parent ClientConn.
	// The balancer should set the same custom user agent if it creates a
	// ClientConn.
	CustomUserAgent string
	// Target contains the parsed address info of the dial target. It is the same resolver.Target as
	// passed to the resolver.
	// See the documentation for the resolver.Target type for details about what it contains.
	Target resolver.Target
}

// Builder 创建一个balancer
type Builder interface {
	// Build 为ClientConn创建一个新的balancer
	Build(cc ClientConn, opts BuildOptions) Balancer
	// Name 返回被此builder构建的balancer的名称
	// 他将会被用于选择balancer(如在service config配置中)
	Name() string
}

// ConfigParser解析负载均衡的配置
type ConfigParser interface {
	// ParseConfig将提供的JSON负载均衡器配置解析为内部形式，如果配置无效，则返回错误。
	//// 为了将来的兼容性，应忽略配置中的未知字段。
	ParseConfig(LoadBalancingConfigJSON json.RawMessage) (serviceconfig.LoadBalancingConfig, error)
}

// PickInfo 包含Pick操作的附加信息
type PickInfo struct {
	// FullMethodName是调用NewClientStream()时的方法名称。 规范格式为/service/Method。
	FullMethodName string
	// Ctx is the RPC's context, and may contain relevant RPC-level information
	// like the outgoing header metadata.
	Ctx context.Context
}

// DoneInfo 包含done的附加信息
type DoneInfo struct {
	// Err RPC完成时的rpc错误，可能为nil
	Err error
	// Trailer contains the metadata from the RPC's trailer, if present.
	Trailer metadata.MD
	// BytesSent表示是否已将任何字节发送到服务器。
	BytesSent bool
	// BytesReceived表示是否已从服务器接收到任何字节。
	BytesReceived bool
	// ServerLoad is the load received from server. It's usually sent as part of
	// trailing metadata.
	//
	// The only supported type now is *orca_v1.LoadReport.
	ServerLoad interface{}
}

var (
	// ErrNoSubConnAvailable indicates no SubConn is available for pick().
	// gRPC will block the RPC until a new picker is available via UpdateState().
	ErrNoSubConnAvailable = errors.New("no SubConn is available")
	// ErrTransientFailure indicates all SubConns are in TransientFailure.
	// WaitForReady RPCs will block, non-WaitForReady RPCs will fail.
	//
	// Deprecated: return an appropriate error based on the last resolution or
	// connection attempt instead.  The behavior is the same for any non-gRPC
	// status error.
	ErrTransientFailure = errors.New("all SubConns are in TransientFailure")
)

// PickResult包含与为RPC选择的连接有关的信息。
type PickResult struct {
	// SubConn是用于此选择的连接（如果其状态为Ready）。如果状态不是Ready，则gRPC将阻塞RPC，直到balancer提供新的Picker为止（使用ClientConn.UpdateState）。
	// SubConn必须是ClientConn.NewSubConn返回的。
	SubConn SubConn
	// Done 当RPC完成时被调用。如果SubConn非ready，调用此方法参数为nil。
	// 如果SubConn不是一个有效的类型，Done可能不会被调用。
	// 如果RPC完成时balancer不希望被通知则Done可能为nil
	Done func(DoneInfo)
}

// TransientFailureError returns e.  It exists for backward compatibility and
// will be deleted soon.
//
// Deprecated: no longer necessary, picker errors are treated this way by
// default.
func TransientFailureError(e error) error { return e }


// gRPC使用Picker来选择SubConn以发送RPC。
// 每当内部状态发生变化时，Balancer就会从其快照中生成一个新的picker。
// gRPC使用的pickers可以通过ClientConn.UpdateState()进行更新。
type Picker interface {
	// Pick返回当前RPC使用的连接和相关信息。
	// Pick不应该被阻塞。如果balancer需要执行I/O操作或任何阻塞或耗时操作来服务此调用，则将返回ErrNoSubConnAvailable
	// 并且当Picker被更新(使用ClientConn.UpdateState)时，Pick将被gRPC重复调用
	// 如果返回错误:
	//
	// - 如果错误为ErrNoSubConnAvailable, gRPC 将会阻塞直到balancer(使用ClientConn.UpdateState)提供一个新的Picker
	// - 如果错误为一个状态错误(被grpc/status包实现)，gRPC将会使用提供code和message结束RPC
	// - 对于所有的其他错误，等待就绪的RPC将等待，但是不等待就绪的RPC将终止，并显示此错误的Error()字符串和不可用状态代码。
	Pick(info PickInfo) (PickResult, error)
}

// Balancer从gRPC接收输入，管理SubConns，并收集和聚合连接状态。
// 它还会生成并更新gRPC用来选择RPC的SubConns的Picker。
// 确保从同一goroutine同步调用UpdateClientConnState，ResolverError，UpdateSubConnState和Close。 Picker.Pick不能保证，可以随时调用。
type Balancer interface {
	// UpdateClientConnState 当ClientConn状态变更被gRPC调用。如果返回的错误是ErrBadResolverState，
	// 则ClientConn将开始以指数退避的方式在active name resolver上调用ResolveNow，
	// 直到对UpdateClientConnState的后续调用返回错误为nil为止。 当前将忽略任何其他错误。
	UpdateClientConnState(ClientConnState) error
	// ResolverError 当name resolver报告错误时被gRPC调用
	ResolverError(error)
	// UpdateSubConnState 当其中一个SubConn的状态变更时被gRPC调用
	UpdateSubConnState(SubConn, SubConnState)
	// Close 关闭这个balancer.balancer不需要为其现有的SubConns调用ClientConn.RemoveSubConn。
	Close()
}

// SubConnState 藐视SubConn的状态
type SubConnState struct {
	// ConnectivityState SubConn的连接状态
	ConnectivityState connectivity.State
	// ConnectionError 如果ConnectivityState状态为TransientFailure设置，来描述SubConn失败的原因。否则为nil
	ConnectionError error
}

// ClientConnState 描述与balancer相关的ClientConn的状态。
type ClientConnState struct {
	ResolverState resolver.State
	// 由builder的ParseConfig方法(如果实现的话)返回的已解析的负载平衡配置。
	BalancerConfig serviceconfig.LoadBalancingConfig
}

// ErrBadResolverState may be returned by UpdateClientConnState to indicate a
// problem with the provided name resolver data.
var ErrBadResolverState = errors.New("bad resolver state")

// ConnectivityStateEvaluator takes the connectivity states of multiple SubConns
// and returns one aggregated connectivity state.
//
// It's not thread safe.
type ConnectivityStateEvaluator struct {
	numReady      uint64 // Number of addrConns in ready state.
	numConnecting uint64 // Number of addrConns in connecting state.
}

// RecordTransition records state change happening in subConn and based on that
// it evaluates what aggregated state should be.
//
//  - If at least one SubConn in Ready, the aggregated state is Ready;
//  - Else if at least one SubConn in Connecting, the aggregated state is Connecting;
//  - Else the aggregated state is TransientFailure.
//
// Idle and Shutdown are not considered.
// RecordTransition记录在subConn中发生的状态更改，并基于此状态更改来评估聚合应该处于什么状态。
// 	-如果至少有一个SubConn处于“就绪”状态，则聚合状态为“就绪”；
// 	-如果连接中至少有一个SubConn，则聚合状态为“连接”；
// 	-其他聚合状态为TransientFailure。
// 不考虑Idle和Shutdown。
func (cse *ConnectivityStateEvaluator) RecordTransition(oldState, newState connectivity.State) connectivity.State {
	// Update counters.
	for idx, state := range []connectivity.State{oldState, newState} {
		updateVal := 2*uint64(idx) - 1 // -1 for oldState and +1 for new.
		switch state {
		case connectivity.Ready:
			cse.numReady += updateVal
		case connectivity.Connecting:
			cse.numConnecting += updateVal
		}
	}

	// Evaluate.
	if cse.numReady > 0 {
		return connectivity.Ready
	}
	if cse.numConnecting > 0 {
		return connectivity.Connecting
	}
	return connectivity.TransientFailure
}
