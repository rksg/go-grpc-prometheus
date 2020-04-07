// Copyright 2016 Michal Witkowski. All Rights Reserved.
// See LICENSE for licensing terms.

package grpc_prometheus

import (
	"time"

	prom "github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/codes"
)

type serverReporter struct {
	metrics     *ServerMetrics
	rpcType     grpcType
	serviceName string
	methodName  string
	startTime   time.Time
	mlisaLabels *MlisaLabels
}

func newServerReporter(m *ServerMetrics, rpcType grpcType, fullMethod string, mlisaLabels *MlisaLabels) *serverReporter {
	r := &serverReporter{
		metrics:     m,
		rpcType:     rpcType,
		mlisaLabels: mlisaLabels,
	}
	if r.metrics.serverHandledHistogramEnabled {
		r.startTime = time.Now()
	}
	r.serviceName, r.methodName = splitMethodName(fullMethod)
	r.incrementVectorWithLabels(r.metrics.serverStartedCounter)
	return r
}

func (r *serverReporter) ReceivedMessage() {
	r.incrementVectorWithLabels(r.metrics.serverStreamMsgReceived)
}

func (r *serverReporter) SentMessage() {
	r.incrementVectorWithLabels(r.metrics.serverStreamMsgSent)
}

func (r *serverReporter) Handled(code codes.Code) {
	r.incrementVectorWithLabels(r.metrics.serverHandledCounter)

	if r.metrics.serverHandledHistogramEnabled {
		r.metrics.serverHandledHistogram.WithLabelValues(string(r.rpcType), r.serviceName, r.methodName).Observe(time.Since(r.startTime).Seconds())
	}
}

func (r *serverReporter) incrementVectorWithLabels(vec *prom.CounterVec) {
	if r.mlisaLabels == nil {
		vec.WithLabelValues(string(r.rpcType), r.serviceName, r.methodName, "unknown", "unknown")
	} else {
		vec.WithLabelValues(string(r.rpcType), r.serviceName, r.methodName, r.mlisaLabels.Topic, r.mlisaLabels.ClusterID)
	}
}
