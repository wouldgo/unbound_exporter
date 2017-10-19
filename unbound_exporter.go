// Copyright 2017 Kumina, https://kumina.nl/
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/log"
	"sort"
)

var (
	unboundUpDesc = prometheus.NewDesc(
		prometheus.BuildFQName("unbound", "", "up"),
		"Whether scraping Unbound's metrics was successful.",
		nil, nil)

	unboundHistogram = prometheus.NewDesc(
		prometheus.BuildFQName("unbound", "", "response_time_seconds"),
		"Query response time in seconds.",
		nil, nil)

	unboundMetrics = []*unboundMetric{
		newUnboundMetric(
			"answer_rcodes_total",
			"Total number of answers to queries, from cache or from recursion, by response code.",
			prometheus.CounterValue,
			[]string{"rcode"},
			"^num\\.answer\\.rcode\\.(\\w+)$"),
		newUnboundMetric(
			"answers_bogus",
			"Total number of answers that were bogus.",
			prometheus.CounterValue,
			nil,
			"^num\\.answer\\.bogus$"),
		newUnboundMetric(
			"answers_secure_total",
			"Total number of answers that were secure.",
			prometheus.CounterValue,
			nil,
			"^num\\.answer\\.secure$"),
		newUnboundMetric(
			"cache_hits_total",
			"Total number of queries that were successfully answered using a cache lookup.",
			prometheus.CounterValue,
			[]string{"thread"},
			"^thread(\\d+)\\.num\\.cachehits$"),
		newUnboundMetric(
			"cache_misses_total",
			"Total number of cache queries that needed recursive processing.",
			prometheus.CounterValue,
			[]string{"thread"},
			"^thread(\\d+)\\.num\\.cachemiss$"),
		newUnboundMetric(
			"memory_caches_bytes",
			"Memory in bytes in use by caches.",
			prometheus.GaugeValue,
			[]string{"cache"},
			"^mem\\.cache\\.(\\w+)$"),
		newUnboundMetric(
			"memory_modules_bytes",
			"Memory in bytes in use by modules.",
			prometheus.GaugeValue,
			[]string{"module"},
			"^mem\\.mod\\.(\\w+)$"),
		newUnboundMetric(
			"memory_sbrk_bytes",
			"Memory in bytes allocated through sbrk.",
			prometheus.GaugeValue,
			nil,
			"^mem\\.total\\.sbrk$"),
		newUnboundMetric(
			"prefetches_total",
			"Total number of cache prefetches performed.",
			prometheus.CounterValue,
			[]string{"thread"},
			"^thread(\\d+)\\.num\\.prefetch$"),
		newUnboundMetric(
			"queries_total",
			"Total number of queries received.",
			prometheus.CounterValue,
			[]string{"thread"},
			"^thread(\\d+)\\.num\\.queries$"),
		newUnboundMetric(
			"query_classes_total",
			"Total number of queries with a given query class.",
			prometheus.CounterValue,
			[]string{"class"},
			"^num\\.query\\.class\\.([\\w]+)$"),
		newUnboundMetric(
			"query_flags_total",
			"Total number of queries that had a given flag set in the header.",
			prometheus.CounterValue,
			[]string{"flag"},
			"^num\\.query\\.flags\\.([\\w]+)$"),
		newUnboundMetric(
			"query_ipv6_total",
			"Total number of queries that were made using IPv6 towards the Unbound server.",
			prometheus.CounterValue,
			nil,
			"^num\\.query\\.ipv6$"),
		newUnboundMetric(
			"query_opcodes_total",
			"Total number of queries with a given query opcode.",
			prometheus.CounterValue,
			[]string{"opcode"},
			"^num\\.query\\.opcode\\.([\\w]+)$"),
		newUnboundMetric(
			"query_edns_DO_total",
			"Total number of queries that had an EDNS OPT record with the DO (DNSSEC OK) bit set present.",
			prometheus.CounterValue,
			nil,
			"^num\\.query\\.edns\\.DO$"),
		newUnboundMetric(
			"query_edns_present_total",
			"Total number of queries that had an EDNS OPT record present.",
			prometheus.CounterValue,
			nil,
			"^num\\.query\\.edns\\.present$"),
		newUnboundMetric(
			"query_tcp_total",
			"Total number of queries that were made using TCP towards the Unbound server.",
			prometheus.CounterValue,
			nil,
			"^num\\.query\\.tcp$"),
		newUnboundMetric(
			"query_types_total",
			"Total number of queries with a given query type.",
			prometheus.CounterValue,
			[]string{"type"},
			"^num\\.query\\.type\\.([\\w]+)$"),
		newUnboundMetric(
			"request_list_current_all",
			"Current size of the request list, including internally generated queries.",
			prometheus.GaugeValue,
			[]string{"thread"},
			"^thread([0-9]+)\\.requestlist\\.current\\.all$"),
		newUnboundMetric(
			"request_list_current_user",
			"Current size of the request list, only counting the requests from client queries.",
			prometheus.GaugeValue,
			[]string{"thread"},
			"^thread([0-9]+)\\.requestlist\\.current\\.user$"),
		newUnboundMetric(
			"request_list_exceeded_total",
			"Number of queries that were dropped because the request list was full.",
			prometheus.CounterValue,
			[]string{"thread"},
			"^thread([0-9]+)\\.requestlist\\.exceeded$"),
		newUnboundMetric(
			"request_list_overwritten_total",
			"Total number of requests in the request list that were overwritten by newer entries.",
			prometheus.CounterValue,
			[]string{"thread"},
			"^thread([0-9]+)\\.requestlist\\.overwritten$"),
		newUnboundMetric(
			"recursive_replies_total",
			"Total number of replies sent to queries that needed recursive processing.",
			prometheus.CounterValue,
			[]string{"thread"},
			"^thread(\\d+)\\.num\\.recursivereplies$"),
		newUnboundMetric(
			"rrset_bogus_total",
			"Total number of rrsets marked bogus by the validator.",
			prometheus.CounterValue,
			nil,
			"^num\\.rrset\\.bogus$"),
		newUnboundMetric(
			"time_elapsed_seconds",
			"Time since last statistics printout in seconds.",
			prometheus.CounterValue,
			nil,
			"^time\\.elapsed$"),
		newUnboundMetric(
			"time_now_seconds",
			"Current time in seconds since 1970.",
			prometheus.GaugeValue,
			nil,
			"^time\\.now$"),
		newUnboundMetric(
			"time_up_seconds_total",
			"Uptime since server boot in seconds.",
			prometheus.CounterValue,
			nil,
			"^time\\.up$"),
		newUnboundMetric(
			"unwanted_queries_total",
			"Total number of queries that were refused or dropped because they failed the access control settings.",
			prometheus.CounterValue,
			nil,
			"^unwanted\\.queries$"),
		newUnboundMetric(
			"unwanted_replies_total",
			"Total number of replies that were unwanted or unsolicited.",
			prometheus.CounterValue,
			nil,
			"^unwanted\\.replies$"),
	}
)

type unboundMetric struct {
	desc      *prometheus.Desc
	valueType prometheus.ValueType
	pattern   *regexp.Regexp
}

func newUnboundMetric(name string, description string, valueType prometheus.ValueType, labels []string, pattern string) *unboundMetric {
	return &unboundMetric{
		desc: prometheus.NewDesc(
			prometheus.BuildFQName("unbound", "", name),
			description,
			labels,
			nil),
		valueType: valueType,
		pattern:   regexp.MustCompile(pattern),
	}
}

func CollectFromReader(file io.Reader, ch chan<- prometheus.Metric) error {
	scanner := bufio.NewScanner(file)
	scanner.Split(bufio.ScanLines)
	histogramPattern := regexp.MustCompile("^histogram\\.(\\d+\\.\\d+)\\.to\\.(\\d+\\.\\d+)$")

	histogramCount := uint64(0)
	histogramSum := float64(0)
	histogramBuckets := make(map[float64]uint64)

	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "=")
		if len(fields) != 2 {
			return fmt.Errorf(
				"%q is not a valid key-value pair",
				scanner.Text())
		}

		for _, metric := range unboundMetrics {
			if matches := metric.pattern.FindStringSubmatch(fields[0]); matches != nil {
				value, err := strconv.ParseFloat(fields[1], 64)
				if err != nil {
					return err
				}
				ch <- prometheus.MustNewConstMetric(
					metric.desc,
					metric.valueType,
					value,
					matches[1:]...)

				break
			}
		}

		if matches := histogramPattern.FindStringSubmatch(fields[0]); matches != nil {
			begin, _ := strconv.ParseFloat(matches[1], 64)
			end, _ := strconv.ParseFloat(matches[2], 64)
			value, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return err
			}
			histogramBuckets[end] = value
			histogramCount += value
			// There are no real data points to calculate the sum in the unbound stats
			// Therefore the mean latency times the amount of samples is calculated and summed
			histogramSum += (end - begin) * float64(value)
		}
	}

	// Convert the metrics to a cumulative prometheus histogram
	keys := []float64{}
	for k := range histogramBuckets {
		keys = append(keys, k)
	}
	sort.Float64s(keys)
	prev := uint64(0)
	for _, i := range keys {
		histogramBuckets[i] += prev
		prev = histogramBuckets[i]
	}
	ch <- prometheus.MustNewConstHistogram(unboundHistogram, histogramCount, histogramSum, histogramBuckets)

	return scanner.Err()
}

func CollectFromFile(path string, ch chan<- prometheus.Metric) error {
	conn, err := os.Open(path)
	if err != nil {
		return err
	}
	return CollectFromReader(conn, ch)
}

func CollectFromSocket(host string, tlsConfig *tls.Config, ch chan<- prometheus.Metric) error {
	conn, err := tls.Dial("tcp", host, tlsConfig)
	if err != nil {
		return err
	}
	_, err = conn.Write([]byte("UBCT1 stats_noreset\n"))
	if err != nil {
		return err
	}
	return CollectFromReader(conn, ch)
}

type UnboundExporter struct {
	host      string
	tlsConfig tls.Config
}

func NewUnboundExporter(host string, ca string, cert string, key string) (*UnboundExporter, error) {
	/* Server authentication. */
	caData, err := ioutil.ReadFile(ca)
	if err != nil {
		return &UnboundExporter{}, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caData) {
		return &UnboundExporter{}, fmt.Errorf("Failed to parse CA")
	}

	/* Client authentication. */
	certData, err := ioutil.ReadFile(cert)
	if err != nil {
		return &UnboundExporter{}, err
	}
	keyData, err := ioutil.ReadFile(key)
	if err != nil {
		return &UnboundExporter{}, err
	}
	keyPair, err := tls.X509KeyPair(certData, keyData)
	if err != nil {
		return &UnboundExporter{}, err
	}

	return &UnboundExporter{
		host: host,
		tlsConfig: tls.Config{
			Certificates: []tls.Certificate{keyPair},
			RootCAs:      roots,
			ServerName:   "unbound",
		},
	}, nil
}

func (e *UnboundExporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- unboundUpDesc
	for _, metric := range unboundMetrics {
		ch <- metric.desc
	}
}

func (e *UnboundExporter) Collect(ch chan<- prometheus.Metric) {
	err := CollectFromSocket(e.host, &e.tlsConfig, ch)
	if err == nil {
		ch <- prometheus.MustNewConstMetric(
			unboundUpDesc,
			prometheus.GaugeValue,
			1.0)
	} else {
		log.Error("Failed to scrape socket: %s", err)
		ch <- prometheus.MustNewConstMetric(
			unboundUpDesc,
			prometheus.GaugeValue,
			0.0)
	}
}

func main() {
	var (
		listenAddress = flag.String("web.listen-address", ":9167", "Address to listen on for web interface and telemetry.")
		metricsPath   = flag.String("web.telemetry-path", "/metrics", "Path under which to expose metrics.")
		unboundHost   = flag.String("unbound.host", "localhost:8953", "Unbound control socket hostname and port number.")
		unboundCa     = flag.String("unbound.ca", "/etc/unbound/unbound_server.pem", "Unbound server certificate.")
		unboundCert   = flag.String("unbound.cert", "/etc/unbound/unbound_control.pem", "Unbound client certificate.")
		unboundKey    = flag.String("unbound.key", "/etc/unbound/unbound_control.key", "Unbound client key.")
	)
	flag.Parse()

	log.Info("Starting unbound_exporter")
	exporter, err := NewUnboundExporter(*unboundHost, *unboundCa, *unboundCert, *unboundKey)
	if err != nil {
		panic(err)
	}
	prometheus.MustRegister(exporter)

	http.Handle(*metricsPath, prometheus.Handler())
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`
			<html>
			<head><title>Unbound Exporter</title></head>
			<body>
			<h1>Unbound Exporter</h1>
			<p><a href='` + *metricsPath + `'>Metrics</a></p>
			</body>
			</html>`))
	})
	log.Info("Listening on address:port => ", *listenAddress)
	log.Fatal(http.ListenAndServe(*listenAddress, nil))
}
