package grpc_prometheus

import (
	"context"
	"sync"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/rksg/go-grpc-prometheus/packages/grpcstatus"

	"google.golang.org/grpc"
)

// ServerMetrics represents a collection of metrics to be registered on a
// Prometheus metrics registry for a gRPC server.
type ServerMetrics struct {
	serverStartedCounter          *prom.CounterVec
	serverHandledCounter          *prom.CounterVec
	serverStreamCounter           *prom.GaugeVec
	serverStreamMsgReceived       *prom.CounterVec
	serverStreamMsgSent           *prom.CounterVec
	serverHandledHistogramEnabled bool
	serverHandledHistogramOpts    prom.HistogramOpts
	serverHandledHistogram        *prom.HistogramVec
	extraLabels                   []string
	getKeyLabel                   GetKeyLabel
	mux                           sync.Mutex
	labelMonitorMap               map[string][]*serverReporter
}

type RetrieveExtralLabels func(grpc.ServerStream) ([]string, error)

type GetKeyLabel func([]string) string

// NewServerMetrics returns a ServerMetrics object. Use a new instance of
// ServerMetrics when not using the default Prometheus metrics registry, for
// example when wanting to control which metrics are added to a registry as
// opposed to automatically adding metrics via init functions.
func NewServerMetrics(extraLabels []string, getKeyLabel GetKeyLabel, counterOpts ...CounterOption) *ServerMetrics {
	opts := counterOptions(counterOpts)
	labels := []string{"grpc_type", "grpc_service", "grpc_method"}
	handledLabels := []string{"grpc_type", "grpc_service", "grpc_method", "grpc_code"}

	if extraLabels != nil {
		labels = append(labels, extraLabels...)
		handledLabels = append(handledLabels, extraLabels...)
	}

	return &ServerMetrics{
		extraLabels:     extraLabels,
		getKeyLabel:     getKeyLabel,
		labelMonitorMap: make(map[string][]*serverReporter),
		serverStartedCounter: prom.NewCounterVec(
			opts.apply(prom.CounterOpts{
				Name: "grpc_server_started_total",
				Help: "Total number of RPCs started on the server.",
			}), labels),
		serverHandledCounter: prom.NewCounterVec(
			opts.apply(prom.CounterOpts{
				Name: "grpc_server_handled_total",
				Help: "Total number of RPCs completed on the server, regardless of success or failure.",
			}), handledLabels),
		serverStreamCounter: prom.NewGaugeVec(
			prom.GaugeOpts{
				Name: "grpc_server_stream_total",
				Help: "Total number of streaming RPCs running on the server.",
			}, labels),
		serverStreamMsgReceived: prom.NewCounterVec(
			opts.apply(prom.CounterOpts{
				Name: "grpc_server_msg_received_total",
				Help: "Total number of RPC stream messages received on the server.",
			}), labels),
		serverStreamMsgSent: prom.NewCounterVec(
			opts.apply(prom.CounterOpts{
				Name: "grpc_server_msg_sent_total",
				Help: "Total number of gRPC stream messages sent by the server.",
			}), labels),
		serverHandledHistogramEnabled: false,
		serverHandledHistogramOpts: prom.HistogramOpts{
			Name:    "grpc_server_handling_seconds",
			Help:    "Histogram of response latency (seconds) of gRPC that had been application-level handled by the server.",
			Buckets: prom.DefBuckets,
		},
		serverHandledHistogram: nil,
	}
}

// EnableHandlingTimeHistogram enables histograms being registered when
// registering the ServerMetrics on a Prometheus registry. Histograms can be
// expensive on Prometheus servers. It takes options to configure histogram
// options such as the defined buckets.
func (m *ServerMetrics) EnableHandlingTimeHistogram(opts ...HistogramOption) {
	for _, o := range opts {
		o(&m.serverHandledHistogramOpts)
	}
	if !m.serverHandledHistogramEnabled {
		m.serverHandledHistogram = prom.NewHistogramVec(
			m.serverHandledHistogramOpts,
			[]string{"grpc_type", "grpc_service", "grpc_method"},
		)
	}
	m.serverHandledHistogramEnabled = true
}

// Describe sends the super-set of all possible descriptors of metrics
// collected by this Collector to the provided channel and returns once
// the last descriptor has been sent.
func (m *ServerMetrics) Describe(ch chan<- *prom.Desc) {
	m.serverStartedCounter.Describe(ch)
	m.serverHandledCounter.Describe(ch)
	m.serverStreamCounter.Describe(ch)
	m.serverStreamMsgReceived.Describe(ch)
	m.serverStreamMsgSent.Describe(ch)
	if m.serverHandledHistogramEnabled {
		m.serverHandledHistogram.Describe(ch)
	}
}

// Collect is called by the Prometheus registry when collecting
// metrics. The implementation sends each collected metric via the
// provided channel and returns once the last metric has been sent.
func (m *ServerMetrics) Collect(ch chan<- prom.Metric) {
	m.serverStartedCounter.Collect(ch)
	m.serverHandledCounter.Collect(ch)
	m.serverStreamCounter.Collect(ch)
	m.serverStreamMsgReceived.Collect(ch)
	m.serverStreamMsgSent.Collect(ch)
	if m.serverHandledHistogramEnabled {
		m.serverHandledHistogram.Collect(ch)
	}
}

// UnaryServerInterceptor is a gRPC server-side interceptor that provides Prometheus monitoring for Unary RPCs.
func (m *ServerMetrics) UnaryServerInterceptor() func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		monitor := newServerReporter(m, Unary, info.FullMethod, nil)
		monitor.ReceivedMessage()
		resp, err := handler(ctx, req)
		st, _ := grpcstatus.FromError(err)
		monitor.Handled(st.Code())
		if err == nil {
			monitor.SentMessage()
		}
		return resp, err
	}
}

// StreamServerInterceptor is a gRPC server-side interceptor that provides Prometheus monitoring for Streaming RPCs.
func (m *ServerMetrics) StreamServerInterceptor(retrieveExtraLabels RetrieveExtralLabels) func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {

	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		var monitor *serverReporter
		if extraLabels, err := retrieveExtraLabels(ss); err == nil {
			monitor = newServerReporter(m, streamRPCType(info), info.FullMethod, extraLabels)
			m.saveMetricMonitor(extraLabels, monitor)
		} else {
			unknownExtraLabels := make([]string, len(m.extraLabels))
			for i := 0; i < len(m.extraLabels); i++ {
				unknownExtraLabels[i] = "unknown"
			}
			monitor = newServerReporter(m, streamRPCType(info), info.FullMethod, unknownExtraLabels)
		}
		err := handler(srv, &monitoredServerStream{ss, monitor})
		st, _ := grpcstatus.FromError(err)
		monitor.Handled(st.Code())
		return err
	}
}

func (m *ServerMetrics) saveMetricMonitor(extraLabels []string, monitor *serverReporter) {
	if m.getKeyLabel != nil {
		m.mux.Lock()
		defer m.mux.Unlock()
		keyLabel := m.getKeyLabel(extraLabels)
		if keyLabel != "" {
			if _, ok := m.labelMonitorMap[keyLabel]; ok {
				m.labelMonitorMap[keyLabel] = append(m.labelMonitorMap[keyLabel], monitor)
			} else {
				m.labelMonitorMap[keyLabel] = []*serverReporter{monitor}
			}
		}
	}
}

func (m *ServerMetrics) ClearMetricsForKeyLabel(keyLabel string) {
	if m.getKeyLabel != nil {
		m.mux.Lock()
		defer m.mux.Unlock()
		if monitors, ok := m.labelMonitorMap[keyLabel]; ok {
			for _, m := range monitors {
				m.ClearMetrics()
			}
			delete(m.labelMonitorMap, keyLabel)
		}
	}
}

// InitializeMetrics initializes all metrics, with their appropriate null
// value, for all gRPC methods registered on a gRPC server. This is useful, to
// ensure that all metrics exist when collecting and querying.
func (m *ServerMetrics) InitializeMetrics(server *grpc.Server) {
	serviceInfo := server.GetServiceInfo()
	for serviceName, info := range serviceInfo {
		for _, mInfo := range info.Methods {
			preRegisterMethod(m, serviceName, &mInfo)
		}
	}
}

func streamRPCType(info *grpc.StreamServerInfo) grpcType {
	if info.IsClientStream && !info.IsServerStream {
		return ClientStream
	} else if !info.IsClientStream && info.IsServerStream {
		return ServerStream
	}
	return BidiStream
}

// monitoredStream wraps grpc.ServerStream allowing each Sent/Recv of message to increment counters.
type monitoredServerStream struct {
	grpc.ServerStream
	monitor *serverReporter
}

func (s *monitoredServerStream) SendMsg(m interface{}) error {
	err := s.ServerStream.SendMsg(m)
	if err == nil {
		s.monitor.SentMessage()
	}
	return err
}

func (s *monitoredServerStream) RecvMsg(m interface{}) error {
	err := s.ServerStream.RecvMsg(m)
	if err == nil {
		s.monitor.ReceivedMessage()
	}
	return err
}

// preRegisterMethod is invoked on Register of a Server, allowing all gRPC services labels to be pre-populated.
func preRegisterMethod(metrics *ServerMetrics, serviceName string, mInfo *grpc.MethodInfo) {
	methodName := mInfo.Name
	methodType := string(typeFromMethodInfo(mInfo))
	// These are just references (no increments), as just referencing will create the labels but not set values.
	metrics.serverStartedCounter.GetMetricWithLabelValues(methodType, serviceName, methodName)
	metrics.serverStreamMsgReceived.GetMetricWithLabelValues(methodType, serviceName, methodName)
	metrics.serverStreamMsgSent.GetMetricWithLabelValues(methodType, serviceName, methodName)
	if metrics.serverHandledHistogramEnabled {
		metrics.serverHandledHistogram.GetMetricWithLabelValues(methodType, serviceName, methodName)
	}
	for _, code := range allCodes {
		metrics.serverHandledCounter.GetMetricWithLabelValues(methodType, serviceName, methodName, code.String())
	}
}
