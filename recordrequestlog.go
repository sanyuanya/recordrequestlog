package recordrequestlog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/trace"
)

var logger *slog.Logger

type Reply struct {
	Data string `json:"data"`
	Msg  string `json:"errmsg"`
	Code int64  `json:"errcode"`
}

func NewReply(data string, msg string, code int64) *Reply {
	return &Reply{
		Data: data,
		Msg:  msg,
		Code: code,
	}
}

type Config struct {
	Endpoint      string `yaml:"endpoint,omitempty"`
	Authorization string `yaml:"authorization,omitempty"`
	Organization  string `yaml:"organization,omitempty"`
	StreamName    string `yaml:"stream_name,omitempty"`
	ServerName    string `yaml:"server_name,omitempty"`
}

func CreateConfig() *Config {
	return &Config{}
}

type RecordRequestLog struct {
	next          http.Handler
	endpoint      string
	authorization string
	organization  string
	streamName    string
	serverName    string
}

func New(ctx context.Context, next http.Handler, config *Config, name string) (http.Handler, error) {

	return &RecordRequestLog{
		next:          next,
		endpoint:      config.Endpoint,
		authorization: config.Authorization,
		organization:  config.Organization,
		streamName:    config.StreamName,
		serverName:    config.ServerName,
	}, nil
}

func (e *RecordRequestLog) ServeHTTP(rw http.ResponseWriter, req *http.Request) {

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)

	defer stop()

	otelShutdown, err := e.setupOTelSDK(ctx)

	if err != nil {
		json.NewEncoder(rw).Encode(NewReply("", err.Error(), http.StatusInternalServerError))
		return
	}

	defer func() {
		err = errors.Join(err, otelShutdown(context.Background()))
	}()

	logger := otelslog.NewLogger(e.serverName)

	var body []byte

	if req.Method == http.MethodPost {
		// 读取请求的内容
		body, err = io.ReadAll(req.Body)

		if err != nil {
			json.NewEncoder(rw).Encode(NewReply("", err.Error(), http.StatusInternalServerError))
			return
		}
	}
	logger.InfoContext(ctx,
		string(body),
		"level", "info",
		"method", req.Method,
		"url", req.URL.String(),
		"host", req.Host,
		"user-agent", req.UserAgent(),
		"appid", req.Header.Get("AppId"),
		"service", e.serverName,
	)

	// 将读取的内容重新放回请求体，以便下一个处理器可以读取
	req.Body = io.NopCloser(bytes.NewBuffer(body))
	e.next.ServeHTTP(rw, req)

	
}

func (e *RecordRequestLog) setupOTelSDK(ctx context.Context) (shutdown func(context.Context) error, err error) {

	var shutdownFuncs []func(context.Context) error

	shutdown = func(ctx context.Context) error {
		var err error

		for _, fn := range shutdownFuncs {
			err = errors.Join(err, fn(ctx))
		}

		shutdownFuncs = nil
		return err
	}

	handleErr := func(inErr error) {
		err = errors.Join(inErr, shutdown(ctx))
	}

	// 设置传播器
	prop := newPropagator()
	otel.SetTextMapPropagator(prop)

	// 设置 trace provider

	traceProvider, err := e.newTraceProvider()

	if err != nil {
		handleErr(err)
		return
	}

	shutdownFuncs = append(shutdownFuncs, traceProvider.Shutdown)
	otel.SetTracerProvider(traceProvider)

	meterProvider, err := e.newMeterProvider()

	if err != nil {
		handleErr(err)
		return
	}

	shutdownFuncs = append(shutdownFuncs, meterProvider.Shutdown)
	otel.SetMeterProvider(meterProvider)

	loggerProvider, err := e.newLoggerProvider()

	if err != nil {
		handleErr(err)
		return
	}

	shutdownFuncs = append(shutdownFuncs, loggerProvider.Shutdown)
	global.SetLoggerProvider(loggerProvider)
	return
}

func newPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
}

func (e *RecordRequestLog) newTraceProvider() (*trace.TracerProvider, error) {

	exp, err := otlptracegrpc.New(context.Background(),
		otlptracegrpc.WithEndpointURL(e.endpoint),
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithHeaders(map[string]string{
			"Authorization": e.authorization,
			"organization":  e.organization,
			"stream-name":   e.streamName,
		}),
	)
	if err != nil {
		return nil, err
	}

	traceProvider := trace.NewTracerProvider(
		trace.WithBatcher(exp,
			trace.WithBatchTimeout(time.Second)),
	)
	return traceProvider, nil
}

func (e *RecordRequestLog) newMeterProvider() (*metric.MeterProvider, error) {

	exp, err := otlpmetricgrpc.New(context.Background(),
		otlpmetricgrpc.WithEndpointURL(e.endpoint),
		otlpmetricgrpc.WithInsecure(),
		otlpmetricgrpc.WithHeaders(map[string]string{
			"Authorization": e.authorization,
			"organization":  e.organization,
			"stream-name":   e.streamName,
		}),
	)

	if err != nil {
		return nil, err
	}

	meterProvider := metric.NewMeterProvider(
		metric.WithReader(metric.NewPeriodicReader(exp,
			metric.WithInterval(3*time.Second))),
	)

	return meterProvider, nil
}

func (e *RecordRequestLog) newLoggerProvider() (*log.LoggerProvider, error) {

	ctx := context.Background()
	exp, err := otlploggrpc.New(ctx,
		otlploggrpc.WithEndpointURL(e.endpoint),
		otlploggrpc.WithInsecure(),
		otlploggrpc.WithHeaders(map[string]string{
			"Authorization": e.authorization,
			"organization":  e.organization,
			"stream-name":   e.streamName,
		}),
	)
	if err != nil {
		return nil, err
	}

	loggerProvider := log.NewLoggerProvider(
		log.WithProcessor(log.NewBatchProcessor(exp)),
	)

	return loggerProvider, nil
}
