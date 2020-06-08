// Copyright 2016 Michal Witkowski. All Rights Reserved.
// See LICENSE for licensing terms.

package grpc_prometheus

import (
	"time"

	"google.golang.org/grpc/codes"
)

type serverReporter struct {
	metrics     *ServerMetrics
	rpcType     grpcType
	serviceName string
	methodName  string
	startTime   time.Time
	allLabels   []string
	extraLabels []string
}

func newServerReporter(m *ServerMetrics, rpcType grpcType, fullMethod string, extraLabels []string) *serverReporter {
	r := &serverReporter{
		metrics: m,
		rpcType: rpcType,
	}
	if r.metrics.serverHandledHistogramEnabled {
		r.startTime = time.Now()
	}
	r.serviceName, r.methodName = splitMethodName(fullMethod)
	r.extraLabels = extraLabels
	r.allLabels = append([]string{string(r.rpcType), r.serviceName, r.methodName}, extraLabels...)

	r.metrics.serverStartedCounter.WithLabelValues(r.allLabels...).Inc()
	r.metrics.serverStreamCounter.WithLabelValues(r.allLabels...).Inc()

	return r
}

func (r *serverReporter) ReceivedMessage() {
	r.metrics.serverStreamMsgReceived.WithLabelValues(r.allLabels...).Inc()
}

func (r *serverReporter) SentMessage() {
	r.metrics.serverStreamMsgSent.WithLabelValues(r.allLabels...).Inc()
}

func (r *serverReporter) Handled(code codes.Code) {
	labels := append([]string{string(r.rpcType), r.serviceName, r.methodName, code.String()}, r.extraLabels...)
	r.metrics.serverHandledCounter.WithLabelValues(labels...).Inc()
	r.metrics.serverStreamCounter.WithLabelValues(labels...).Dec()

	if r.metrics.serverHandledHistogramEnabled {
		r.metrics.serverHandledHistogram.WithLabelValues(string(r.rpcType), r.serviceName, r.methodName).Observe(time.Since(r.startTime).Seconds())
	}
}
