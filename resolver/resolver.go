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

// Package resolver defines APIs for name resolution in gRPC.
// All APIs in this package are experimental.
package resolver

import (
	"context"
	"net"

	"google.golang.org/grpc/attributes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/serviceconfig"
)

var (
	// m is a map from scheme to resolver builder.
	m = make(map[string]Builder)
	// defaultScheme is the default scheme to use.
	defaultScheme = "passthrough"
)

// TODO(bar) install dns resolver in init(){}.

// Register registers the resolver builder to the resolver map. b.Scheme will be
// used as the scheme registered with this builder.
//
// NOTE: this function must only be called during initialization time (i.e. in
// an init() function), and is not thread-safe. If multiple Resolvers are
// registered with the same name, the one registered last will take effect.
func Register(b Builder) {
	m[b.Scheme()] = b
}

// Get returns the resolver builder registered with the given scheme.
//
// If no builder is register with the scheme, nil will be returned.
func Get(scheme string) Builder {
	if b, ok := m[scheme]; ok {
		return b
	}
	return nil
}

// SetDefaultScheme sets the default scheme that will be used. The default
// default scheme is "passthrough".
//
// NOTE: this function must only be called during initialization time (i.e. in
// an init() function), and is not thread-safe. The scheme set last overrides
// previously set values.
func SetDefaultScheme(scheme string) {
	defaultScheme = scheme
}

// GetDefaultScheme gets the default scheme that will be used.
func GetDefaultScheme() string {
	return defaultScheme
}

// AddressType 表示名称解析返回的地址类型
//
// Deprecated: use Attributes in Address instead.
type AddressType uint8

const (
	// Backend 表示地址对应
	//
	// Deprecated: use Attributes in Address instead.
	Backend AddressType = iota
	// GRPCLB 表示地址对应一个grpclb负载均衡器
	//
	// Deprecated: to select the GRPCLB load balancing policy, use a service
	// config with a corresponding loadBalancingConfig.  To supply balancer
	// addresses to the GRPCLB load balancing policy, set State.Attributes
	// using balancer/grpclb/state.Set.
	GRPCLB
)

// Address 表示客户端连接的服务端地址
//
// Experimental
// 注意此类型为实验性质的，之后的版本可能会修改或移除
// Notice: This type is EXPERIMENTAL and may be changed or removed in a
// later release.
type Address struct {
	// Addr is the server address on which a connection will be established.
	Addr string

	// ServerName is the name of this address.
	// If non-empty, the ServerName is used as the transport certification authority for
	// the address, instead of the hostname from the Dial target string. In most cases,
	// this should not be set.
	//
	// If Type is GRPCLB, ServerName should be the name of the remote load
	// balancer, not the name of the backend.
	//
	// WARNING: ServerName must only be populated with trusted values. It
	// is insecure to populate it with data from untrusted inputs since untrusted
	// values could be used to bypass the authority checks performed by TLS.
	ServerName string

	// Attributes contains arbitrary data about this address intended for
	// consumption by the load balancing policy.
	Attributes *attributes.Attributes

	// Type 地址类型
	//
	// Deprecated: use Attributes instead.
	Type AddressType

	// Metadata Addr相关的信息，可能被用于负载均衡决策
	//
	// Deprecated: use Attributes instead.
	Metadata interface{}
}

// BuildOptions 包含builder创建resolver附加的信息
type BuildOptions struct {
	// DisableServiceConfig indicates whether a resolver implementation should
	// fetch service config data.
	DisableServiceConfig bool
	// DialCreds is the transport credentials used by the ClientConn for
	// communicating with the target gRPC service (set via
	// WithTransportCredentials). In cases where a name resolution service
	// requires the same credentials, the resolver may use this field. In most
	// cases though, it is not appropriate, and this field may be ignored.
	DialCreds credentials.TransportCredentials
	// CredsBundle is the credentials bundle used by the ClientConn for
	// communicating with the target gRPC service (set via
	// WithCredentialsBundle). In cases where a name resolution service
	// requires the same credentials, the resolver may use this field. In most
	// cases though, it is not appropriate, and this field may be ignored.
	CredsBundle credentials.Bundle
	// Dialer is the custom dialer used by the ClientConn for dialling the
	// target gRPC service (set via WithDialer). In cases where a name
	// resolution service requires the same dialer, the resolver may use this
	// field. In most cases though, it is not appropriate, and this field may
	// be ignored.
	Dialer func(context.Context, string) (net.Conn, error)
}

// State包含与ClientConn相关的当前Resolver的状态。
type State struct {
	// Addresses是target最新已解析的地址集
	Addresses []Address
	// ServiceConfig包含解析最新服务配置的结果。 如果为nil，则表示不存在任何服务配置或resolver不提供服务配置。
	ServiceConfig *serviceconfig.ParseResult
	// Attributes包含由负载平衡策略使用的resolver相关的任意数据。
	Attributes *attributes.Attributes
}

// ClientConn包含用于resolver的回调，以通知对gRPC ClientConn的任何更新。
// 该接口将由gRPC实现。 用户不需要该接口的全新实现。 对于类似测试的情况，新的实现应嵌入此接口。 这使gRPC可以向该接口添加新方法。
type ClientConn interface {
	// UpdateState适当地更新ClientConn的状态。
	UpdateState(State)
	// ReportError通知ClientConn,解Resolver遇到错误。 ClientConn将通知load balancer，并开始以指数退避的方式在Resolver上调用ResolveNow。
	ReportError(error)
	// NewAddress被resolver调用，以通知ClientConn新解析出来的地址列表
	// 这个地址列表应该是完整的解析地址列表
	// 已废弃: 使用UpdateState方法替换
	NewAddress(addresses []Address)
	// NewServiceConfig被resolver调用，以通知ClientConn新的服务配置，服务配置应该是json格式的字符串
	// 已废弃: 使用UpdateState方法替换
	NewServiceConfig(serviceConfig string)
	// ParseServiceConfig解析提供的服务配置并返回一个提供解析后的配置的对象
	ParseServiceConfig(serviceConfigJSON string) *serviceconfig.ParseResult
}

// Target represents a target for gRPC, as specified in:
// https://github.com/grpc/grpc/blob/master/doc/naming.md.
// It is parsed from the target string that gets passed into Dial or DialContext by the user. And
// grpc passes it to the resolver and the balancer.
//
// If the target follows the naming spec, and the parsed scheme is registered with grpc, we will
// parse the target string according to the spec. e.g. "dns://some_authority/foo.bar" will be parsed
// into &Target{Scheme: "dns", Authority: "some_authority", Endpoint: "foo.bar"}
//
// If the target does not contain a scheme, we will apply the default scheme, and set the Target to
// be the full target string. e.g. "foo.bar" will be parsed into
// &Target{Scheme: resolver.GetDefaultScheme(), Endpoint: "foo.bar"}.
//
// If the parsed scheme is not registered (i.e. no corresponding resolver available to resolve the
// endpoint), we set the Scheme to be the default scheme, and set the Endpoint to be the full target
// string. e.g. target string "unknown_scheme://authority/endpoint" will be parsed into
// &Target{Scheme: resolver.GetDefaultScheme(), Endpoint: "unknown_scheme://authority/endpoint"}.
// Target代表gRPC的目标
// 它是从用户传递到Dial或DialContext的目标字符串中解析得到。 然后gRPC将其传递给resolver和balancer。
// 如果目标遵循命名规范，并且解析的scheme已向gRPC注册，则我们将根据规范解析目标字符串。
// 例如 “ dns：//some_authority/foo.bar”将被解析为&Target {Scheme:"dns",Authority:"some_authority",Endpoint:"foo.bar"}
// 如果目标不包含scheme，我们将应用默认scheme，并将Target设置为完整的目标字符串。 例如 "foo.bar"将解析为&Target{Scheme：resolver.GetDefaultScheme(),Endpoint: "foo.bar"}。
// 如果已解析的scheme未注册（即没有相应的resolver可用于解析endpoint），则将Scheme设置为默认方案，并将Endpoint设置为完整的目标字符串。
// 例如 目标字符串 "unknown_scheme://authority/endpoint"将被解析为&Target{Scheme: resolver.GetDefaultScheme(), Endpoint: "unknown_scheme://authority/endpoint"}。
type Target struct {
	Scheme    string
	Authority string
	Endpoint  string
}

// Builder将创建一个解析器，该解析器将用于监视名称解析更新。
type Builder interface {
	// Build 为提供的target创建一个新的resolver
	// gRPC dial同步调用Build方法 如果返回error不为nil则构建失败
	Build(target Target, cc ClientConn, opts BuildOptions) (Resolver, error)
	// Scheme 返回当前resolver支持的scheme
	// Scheme is defined at https://github.com/grpc/grpc/blob/master/doc/naming.md.
	Scheme() string
}

// ResolveNowOptions includes additional information for ResolveNow.
type ResolveNowOptions struct{}

// Resolver watches for the updates on the specified target.
// Updates include address updates and service config updates.
// Resolver程序监视指定target上的更新。
// 更新包括地址更新和服务配置更新。
type Resolver interface {
	// gRPC将调用ResolveNow尝试再次解析目标名称。 这只是一个提示，如果不是必须的，resolver可以忽略它。
	// 可以同时调用多次。
	ResolveNow(ResolveNowOptions)
	// Close closes the resolver.
	Close()
}

// UnregisterForTesting removes the resolver builder with the given scheme from the
// resolver map.
// This function is for testing only.
func UnregisterForTesting(scheme string) {
	delete(m, scheme)
}
