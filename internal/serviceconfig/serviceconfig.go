/*
 *
 * Copyright 2020 gRPC authors.
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

// Package serviceconfig contains utility functions to parse service config.
package serviceconfig

import (
	"encoding/json"
	"fmt"
	"time"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/grpclog"
	externalserviceconfig "google.golang.org/grpc/serviceconfig"
)

var logger = grpclog.Component("core")

// BalancerConfig wraps the name and config associated with one load balancing
// policy. It corresponds to a single entry of the loadBalancingConfig field
// from ServiceConfig.
//
// It implements the json.Unmarshaler interface.
//
// https://github.com/grpc/grpc-proto/blob/54713b1e8bc6ed2d4f25fb4dff527842150b91b2/grpc/service_config/service_config.proto#L247
type BalancerConfig struct {
	Name   string
	Config externalserviceconfig.LoadBalancingConfig
}

type intermediateBalancerConfig []map[string]json.RawMessage

// UnmarshalJSON implements the json.Unmarshaler interface.
//
// ServiceConfig contains a list of loadBalancingConfigs, each with a name and
// config. This method iterates through that list in order, and stops at the
// first policy that is supported.
// - If the config for the first supported policy is invalid, the whole service
//   config is invalid.
// - If the list doesn't contain any supported policy, the whole service config
//   is invalid.
func (bc *BalancerConfig) UnmarshalJSON(b []byte) error {
	var ir intermediateBalancerConfig
	err := json.Unmarshal(b, &ir)
	if err != nil {
		return err
	}

	for i, lbcfg := range ir {
		if len(lbcfg) != 1 {
			return fmt.Errorf("invalid loadBalancingConfig: entry %v does not contain exactly 1 policy/config pair: %q", i, lbcfg)
		}

		var (
			name    string
			jsonCfg json.RawMessage
		)
		// Get the key:value pair from the map. We have already made sure that
		// the map contains a single entry.
		for name, jsonCfg = range lbcfg {
		}

		builder := balancer.Get(name)
		if builder == nil {
			// If the balancer is not registered, move on to the next config.
			// This is not an error.
			continue
		}
		bc.Name = name

		parser, ok := builder.(balancer.ConfigParser)
		if !ok {
			if string(jsonCfg) != "{}" {
				logger.Warningf("non-empty balancer configuration %q, but balancer does not implement ParseConfig", string(jsonCfg))
			}
			// Stop at this, though the builder doesn't support parsing config.
			return nil
		}

		cfg, err := parser.ParseConfig(jsonCfg)
		if err != nil {
			return fmt.Errorf("error parsing loadBalancingConfig for policy %q: %v", name, err)
		}
		bc.Config = cfg
		return nil
	}
	// This is reached when the for loop iterates over all entries, but didn't
	// return. This means we had a loadBalancingConfig slice but did not
	// encounter a registered policy. The config is considered invalid in this
	// case.
	return fmt.Errorf("invalid loadBalancingConfig: no supported policies found")
}

// MethodConfig定义了服务提供者针对特定方法推荐的配置
type MethodConfig struct {
	// WaitForReady表示发送到此方法的RPC是否应等待，直到默认情况下连接准备就绪（!failfast）。
	// 通过gRPC客户端API指定的值将覆盖此处设置的值。
	WaitForReady *bool
	// Timeout是发送到此方法的RPC的默认超时。 实际使用的Timeout将是此处指定的值和应用程序通过gRPC客户端API设置的值的最小值。
	// 如果未设置任何一个，则将使用另一个。 如果两者均未设置，则RPC没有截止日期。
	Timeout *time.Duration
	// MaxReqSize MaxReqSize是流（客户端->服务器）中单个请求允许的最大有效负载大小（以字节为单位）。
	// 测量的大小是按消息压缩后（但在流压缩之前）以字节为单位的序列化有效负载。 使用的实际值是此处指定的值和应用程序通过gRPC客户端API设置的值的最小值。
	// 如果未设置任何一个，则将使用另一个。 如果两者均未设置，则使用内置默认值。
	MaxReqSize *int
	// MaxRespSize是流（服务器->客户端）中单个响应的最大允许有效负载大小（以字节为单位）。
	MaxRespSize *int
	// RetryPolicy 为方法配置重试选项
	RetryPolicy *RetryPolicy
}

// RetryPolicy defines the go-native version of the retry policy defined by the
// service config here:
// https://github.com/grpc/proposal/blob/master/A6-client-retries.md#integration-with-service-config
type RetryPolicy struct {
	// MaxAttempts is the maximum number of attempts, including the original RPC.
	//
	// This field is required and must be two or greater.
	MaxAttempts int

	// Exponential backoff parameters. The initial retry attempt will occur at
	// random(0, initialBackoff). In general, the nth attempt will occur at
	// random(0,
	//   min(initialBackoff*backoffMultiplier**(n-1), maxBackoff)).
	//
	// These fields are required and must be greater than zero.
	InitialBackoff    time.Duration
	MaxBackoff        time.Duration
	BackoffMultiplier float64

	// The set of status codes which may be retried.
	//
	// Status codes are specified as strings, e.g., "UNAVAILABLE".
	//
	// This field is required and must be non-empty.
	// Note: a set is used to store this for easy lookup.
	RetryableStatusCodes map[codes.Code]bool
}
