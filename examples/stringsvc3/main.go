package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"

	stdprometheus "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/go-kit/kit/log"
	kitprometheus "github.com/go-kit/kit/metrics/prometheus"
	httptransport "github.com/go-kit/kit/transport/http"

	consulsd "github.com/go-kit/kit/sd/consul"
	consulapi "github.com/hashicorp/consul/api"
)

func main() {
	var (
		listen     = flag.Int("listen", 9090, "HTTP listen address")
		proxy      = flag.String("proxy", "", "Optional comma-separated list of URLs to proxy uppercase requests")
		consulAddr = flag.String("consul.addr", "127.0.0.1:8500", "Consul agent address")
	)
	flag.Parse()

	var logger log.Logger
	logger = log.NewLogfmtLogger(os.Stderr)
	logger = log.With(logger, "listen", ":"+strconv.Itoa(*listen), "caller", log.DefaultCaller)

	fieldKeys := []string{"method", "error"}
	requestCount := kitprometheus.NewCounterFrom(stdprometheus.CounterOpts{
		Namespace: "my_group",
		Subsystem: "string_service",
		Name:      "request_count",
		Help:      "Number of requests received.",
	}, fieldKeys)
	requestLatency := kitprometheus.NewSummaryFrom(stdprometheus.SummaryOpts{
		Namespace: "my_group",
		Subsystem: "string_service",
		Name:      "request_latency_microseconds",
		Help:      "Total duration of requests in microseconds.",
	}, fieldKeys)
	countResult := kitprometheus.NewSummaryFrom(stdprometheus.SummaryOpts{
		Namespace: "my_group",
		Subsystem: "string_service",
		Name:      "count_result",
		Help:      "The result of each count method.",
	}, []string{})

	// Service discovery domain. In this example we use Consul.
	var client consulsd.Client
	{
		consulConfig := consulapi.DefaultConfig()
		if len(*consulAddr) > 0 {
			consulConfig.Address = *consulAddr
		}
		consulClient, err := consulapi.NewClient(consulConfig)
		if err != nil {
			logger.Log("err", err)
			os.Exit(1)
		}
		client = consulsd.NewClient(consulClient)
	}

	registration := new(consulapi.AgentServiceRegistration)
	registration.ID = "stringsvc_1"
	registration.Name = "stringsvc"
	registration.Port = *listen
	registration.Tags = []string{}
	registration.Address = "192.168.11.5" //使用 docker 部署 consul 集群，无法配置为 127.0.0.1，暂配置为宿主机地址
	registration.Check = &consulapi.AgentServiceCheck{
		HTTP:                           fmt.Sprintf("http://%s:%d%s", registration.Address, *listen, "/metrics"),
		Timeout:                        "3s",
		Interval:                       "5s",
		DeregisterCriticalServiceAfter: "30s", //check失败后30秒删除本服务
	}

	registrar := consulsd.NewRegistrar(client, registration, logger)
	registrar.Register()

	var svc StringService
	svc = stringService{}
	svc = proxyingMiddleware(context.Background(), *proxy, logger)(svc)
	svc = loggingMiddleware(logger)(svc)
	svc = instrumentingMiddleware(requestCount, requestLatency, countResult)(svc)

	uppercaseHandler := httptransport.NewServer(
		makeUppercaseEndpoint(svc),
		decodeUppercaseRequest,
		encodeResponse,
	)
	countHandler := httptransport.NewServer(
		makeCountEndpoint(svc),
		decodeCountRequest,
		encodeResponse,
	)

	http.Handle("/uppercase", uppercaseHandler)
	http.Handle("/count", countHandler)
	http.Handle("/metrics", promhttp.Handler())
	logger.Log("msg", "HTTP", "addr", strconv.Itoa(*listen))
	logger.Log("err", http.ListenAndServe(":"+strconv.Itoa(*listen), nil))
}
