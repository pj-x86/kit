package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"

	"github.com/hudl/fargo"
	stdprometheus "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/go-kit/kit/log"
	logzap "github.com/go-kit/kit/log/zap"
	"go.uber.org/zap"

	kitprometheus "github.com/go-kit/kit/metrics/prometheus"
	httptransport "github.com/go-kit/kit/transport/http"

	consulsd "github.com/go-kit/kit/sd/consul"
	"github.com/go-kit/kit/sd/eureka"
	consulapi "github.com/hashicorp/consul/api"
)

func main() {
	var (
		listen       = flag.Int("listen", 9090, "HTTP listen address")
		proxy        = flag.String("proxy", "", "Optional comma-separated list of URLs to proxy uppercase requests")
		consulAddr   = flag.String("consul.addr", "127.0.0.1:8500", "Consul agent address")
		eurekaAddr   = flag.String("eureka.addr", "127.0.0.1:8761", "Eureka Server address")
		registryType = flag.String("registry.type", "consul", "service registry center[consul, eureka]")
		appIP        = flag.String("app.ip", "192.168.11.5", "application ip")
	)
	flag.Parse()

	var logger log.Logger
	var zapLogger *zap.Logger
	var atomLevel zap.AtomicLevel //日志输出级别，可动态修改
	var env string = "dev"        //dev-开发, test-测试, prod-生产

	atomLevel = zap.NewAtomicLevelAt(zap.InfoLevel)               //默认 Info 日志级别
	zapLogger = logzap.NewZapLogger(env, "./test.log", atomLevel) //默认创建性能最佳的 Logger 对象，对性能有要求的场景下可以使用它，仅支持结构化日志输出

	logger = logzap.NewZapSugarLogger(zapLogger, atomLevel.Level())
	//logger = log.NewLogfmtLogger(os.Stderr)
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

	// Service discovery domain.
	if *registryType == "consul" {
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
		registration.Address = *appIP //使用 docker 部署 consul 集群，无法配置为 127.0.0.1，暂配置为宿主机地址
		registration.Check = &consulapi.AgentServiceCheck{
			HTTP:                           fmt.Sprintf("http://%s:%d%s", registration.Address, *listen, "/metrics"),
			Timeout:                        "3s",
			Interval:                       "5s",
			DeregisterCriticalServiceAfter: "30s", //check失败后30秒删除本服务
		}

		registrar := consulsd.NewRegistrar(client, registration, logger)
		registrar.Register()
	} else if *registryType == "eureka" {
		eurekaURL := fmt.Sprintf("http://%s/eureka", *eurekaAddr)
		var fargoConfig fargo.Config
		fargoConfig.Eureka.ServiceUrls = []string{eurekaURL}
		// 订阅服务器应轮询更新的频率
		fargoConfig.Eureka.PollIntervalSeconds = 30

		instance := &fargo.Instance{
			HostName:         *appIP + ":" + strconv.Itoa(*listen),
			Port:             *listen,
			PortEnabled:      true,
			App:              "stringsvc",
			IPAddr:           *appIP,
			VipAddress:       *appIP,
			HealthCheckUrl:   fmt.Sprintf("http://%s:%d%s", *appIP, *listen, "/metrics"),
			Status:           fargo.UP,
			Overriddenstatus: fargo.UP,
			DataCenterInfo:   fargo.DataCenterInfo{Name: fargo.MyOwn},
			LeaseInfo:        fargo.LeaseInfo{RenewalIntervalInSecs: 30, DurationInSecs: 90},
		}
		fargoConnection := fargo.NewConnFromConfig(fargoConfig)
		registrar := eureka.NewRegistrar(&fargoConnection, instance, logger)

		registrar.Register()
		defer registrar.Deregister()
	}

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

	/* 启用动态修改日志级别的 HTTP 接口
	 * 使用方法：
	 * curl http://localhost:8080/log/level  #获取当前日志级别
	 * curl -XPUT --data '{"level":"debug"}' http://localhost:8080/log/level  #修改日志级别为 debug
	 * 支持的日志级别: debug, info, warn, error, panic, fatal
	 */
	http.HandleFunc("/log/level", atomLevel.ServeHTTP)
	http.Handle("/uppercase", uppercaseHandler)
	http.Handle("/count", countHandler)
	http.Handle("/metrics", promhttp.Handler())
	logger.Log("msg", "HTTP", "addr", strconv.Itoa(*listen))
	logger.Log("err", http.ListenAndServe(":"+strconv.Itoa(*listen), nil))
}
