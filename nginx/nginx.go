/*
Copyright 2016 Staples, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package nginx

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/intelsdi-x/snap/control/plugin"
	"github.com/intelsdi-x/snap/control/plugin/cpolicy"
	"github.com/intelsdi-x/snap/core"
	"github.com/intelsdi-x/snap/core/ctypes"
)

const (
	// Name of plugin
	pluginName = "nginx"
	// Version of plugin
	pluginVersion = 1
	// Type of plugin
	pluginType = plugin.CollectorPluginType

        HTTP_200_OK = 200
        NUM_DOT_IN_IP = 3
        HTTP_TIME_OUT = 5
)

var (
	ErrorCfgParam      = errors.New("nginx_server_url config required. Check your config JSON file")
	ErrorBadServer     = errors.New("Failed to parse given nginx_server_url")
	ErrorRequestFail   = errors.New("Request to nginx server failed")
	ErrorConfigRead    = errors.New("Config read error")
)

// NginxCollector type
type NginxCollector struct{}

// NewNginxCollector returns a NginxCollector struct
func NewNginxCollector() *NginxCollector {
	return &NginxCollector{}
}

//Convert unresolved ip address to md5
func getMD5Hash(text string) string {
	hasher := md5.New()
	hasher.Write([]byte(text))
	return hex.EncodeToString(hasher.Sum(nil))
}

//Get hostname based on server ip address of nginx metric
func getHostName(inData interface{}, hostName string) string {
	flag := false
	switch mtype := inData.(type) {
	case map[string]interface{}:
		hostName = mtype["server"].(string)
		//check for IPV4
		if strings.Count(hostName, ".") == NUM_DOT_IN_IP {
			subStr := strings.Split(hostName, ":")
			hName, err := net.LookupAddr(subStr[0])
			if err == nil {
				hostName = strings.Join(hName, ".")
				flag = true
			}
		} else {
			if strings.Contains(hostName, "::") == true {
				subStr := strings.Split(hostName, "]")
				tStr := strings.TrimLeft(subStr[0], "[")
				hName, err := net.LookupAddr(tStr)
				if err == nil {
					hostName = strings.Join(hName, ".")
					flag = true
				}
			}
		}
	}
	if flag == false {
		//Default hostname with port will be encoded to md5
		hostName = fmt.Sprintf("host_id_%s", getMD5Hash(hostName))
	} else {
		hostName = fmt.Sprintf("host_id_%s", hostName)
	}
	hostName = strings.TrimRight(hostName, ".")
	replacer := strings.NewReplacer(".", "_", "/", "_", "\\", "_", ":", "_", "%", "_")
	hostName = replacer.Replace(hostName)
	return hostName
}

//Will ignore list of mertic
func checkIgnoreMetric(mkey string) bool {
	IgnoreChildMetric := "server"
	IgnoreMetric := ""
	ret := false
	if strings.EqualFold(IgnoreChildMetric, "nil") == false {
		subMetric := strings.Split(mkey, "/")
		if strings.Contains(IgnoreChildMetric, subMetric[len(subMetric)-1]) == true {
			ret = true
		}
	}
	if strings.EqualFold(IgnoreMetric, "nil") == false {
		if strings.Contains(IgnoreMetric, mkey) == true {
			ret = true
		}
	}

	return ret
}

//Namespace convert based on snap requirment
func getNamespace(mkey string) (ns core.Namespace) {
	rc := strings.Replace(mkey, ".", "-", -1)
	ss := strings.Split(rc, "/")
	ns = core.NewNamespace(ss...)
	return ns
}

//Flattern complex json struct metrics
func switchType(outMetric *[]plugin.MetricType, mval interface{}, ak string) {
	switch mtype := mval.(type) {
	case bool:
		if checkIgnoreMetric(ak) == true {
			return
		}
		ns := getNamespace(ak)
		tmp := plugin.MetricType{}
		tmp.Namespace_ = ns
		if mval.(bool) == false {
			tmp.Data_ = 0
		} else {
			tmp.Data_ = 1
		}
		tmp.Timestamp_ = time.Now()
		*outMetric = append(*outMetric, tmp)
	case int, int64, float64, string:
		if checkIgnoreMetric(ak) == true {
			return
		}
		ns := getNamespace(ak)
		tmp := plugin.MetricType{}
		tmp.Namespace_ = ns
		tmp.Data_ = mval
		tmp.Timestamp_ = time.Now()
		*outMetric = append(*outMetric, tmp)
	case map[string]interface{}:
		parseMetrics(outMetric, mtype, ak)
	case []interface{}:
		parseArrMetrics(outMetric, mtype, ak)
	default:
		log.Println("In default missing type =", reflect.TypeOf(mval))
	}
	return
}

//Parse Arrary Metric Data
func parseArrMetrics(outMetric *[]plugin.MetricType, inData []interface{}, parentKey string) {
	for mkey, mval := range inData {
		subMetric := strings.Split(parentKey, "/")
		if subMetric[len(subMetric)-1] == "peers" {
			hostName := getHostName(mval, strconv.Itoa(mkey))
			switchType(outMetric, mval, parentKey+"/"+hostName)
		} else {
			switchType(outMetric, mval, parentKey+"/"+strconv.Itoa(mkey))
		}
	}
	return
}

//Parse Metrics
func parseMetrics(outMetric *[]plugin.MetricType, inData map[string]interface{}, parentKey string) {

	for mkey, mval := range inData {
		switchType(outMetric, mval, parentKey+"/"+mkey)
	}
	return
}

//Get nginx metric from Nginx application
func getMetrics(nginxServer string, metrics []string) (mList []plugin.MetricType, err error) {

	tr := &http.Transport{}

        httptimeout := time.Duration(HTTP_TIME_OUT) * time.Second

	client := &http.Client {
                 Transport: tr,
                 Timeout: httptimeout,
               }

	resp, err1 := client.Get(nginxServer)
	if err1 != nil {
		return nil, err1
	}
        defer resp.Body.Close()

	if resp.StatusCode != HTTP_200_OK {
		return nil, ErrorRequestFail
	}

	body, err2 := ioutil.ReadAll(resp.Body)
	if err2 != nil {
		return nil, err2
	}

	jFmt := make(map[string]interface{})
	err = json.Unmarshal(body, &jFmt)
	if err != nil {
		return nil, err
	}

	pk := "staples" + "/" + "nginx"
	parseMetrics(&mList, jFmt, pk)

	return mList, nil
}

//CollectMetrics API definition
func (n *NginxCollector) CollectMetrics(inmts []plugin.MetricType) ([]plugin.MetricType, error) {

	nginxServerUrlCfg := inmts[0].Config().Table()["nginx_server_url"]
	if nginxServerUrlCfg == nil {
		return nil, ErrorConfigRead
	}

	nginxServerUrl, ok := nginxServerUrlCfg.(ctypes.ConfigValueStr)
	if !ok {
		return nil, ErrorBadServer
	}

	metricsList := make([]string, len(inmts))

	for i, mts := range inmts {
		metricsList[i] = mts.Namespace().String()
	}

	mList, err := getMetrics(nginxServerUrl.Value, metricsList)
	if err != nil {
		log.Println("Error in getMetrics =", err)
	}
	return mList, nil
}

// GetMetricTypes API definition
func (n *NginxCollector) GetMetricTypes(cfg plugin.ConfigType) (mList []plugin.MetricType, err error) {

	nginxServerUrlCfg := cfg.Table()["nginx_server_url"]
	if nginxServerUrlCfg == nil {
		return nil, ErrorConfigRead
	}

	nginxServerUrl, ok := nginxServerUrlCfg.(ctypes.ConfigValueStr)
	if !ok {
		return nil, ErrorBadServer
	}

	mList, err = getMetrics(nginxServerUrl.Value, []string{})
	if err != nil {
		log.Println("Error in getMetrics =", err)
	}
	return mList, nil
}

//GetConfigPolicy API definition
func (n *NginxCollector) GetConfigPolicy() (*cpolicy.ConfigPolicy, error) {
	cfg := cpolicy.New()
	nginxServerUrl, _ := cpolicy.NewStringRule("nginx_server_url", true, "http://localhost/status")
	policy := cpolicy.NewPolicyNode()
	policy.Add(nginxServerUrl)
	cfg.Add([]string{"staples", "nginx"}, policy)
	return cfg, nil
}

//Meta API definition
func Meta() *plugin.PluginMeta {
	return plugin.NewPluginMeta(
		pluginName,
		pluginVersion,
		pluginType,
		[]string{plugin.SnapGOBContentType},
		[]string{plugin.SnapGOBContentType},
		plugin.Unsecure(true),
		plugin.RoutingStrategy(plugin.DefaultRouting),
		plugin.CacheTTL(1100*time.Millisecond),
	)
}
