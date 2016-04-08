package main

import (
	"log"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/prometheus/alertmanager/template"
)

var (
	amDataToEnvMap = map[*template.Data][]string{
		&template.Data{Receiver: "default", Status: "firing", Alerts: template.Alerts{template.Alert{Status: "firing", Labels: template.KV{"job": "broken", "monitor": "codelab-monitor", "alertname": "InstanceDown", "instance": "localhost:1234"}, Annotations: template.KV{}, StartsAt: time.Unix(1460045332, 0), EndsAt: time.Time{}, GeneratorURL: "http://oldpad:9090/graph#%5B%7B%22expr%22%3A%22up%20%3D%3D%200%22%2C%22tab%22%3A0%7D%5D"}}, GroupLabels: template.KV{"alertname": "InstanceDown"}, CommonLabels: template.KV{"alertname": "InstanceDown", "instance": "localhost:1234", "job": "broken", "monitor": "codelab-monitor"}, CommonAnnotations: template.KV{}, ExternalURL: "http://oldpad:9093"}: []string{"ALERT_0_END=-62135596800", "ALERT_0_LABEL_alertname=InstanceDown", "ALERT_0_LABEL_instance=localhost:1234", "ALERT_0_LABEL_job=broken", "ALERT_0_LABEL_monitor=codelab-monitor", "ALERT_0_START=1460045332", "ALERT_0_STATUS=firing", "ALERT_0_URL=http://oldpad:9090/graph#%5B%7B%22expr%22%3A%22up%20%3D%3D%200%22%2C%22tab%22%3A0%7D%5D", "ALERT_LEN=1", "EXTERNAL_URL=http://oldpad:9093", "GLABEL_alertname=InstanceDown", "LABEL_alertname=InstanceDown", "LABEL_instance=localhost:1234", "LABEL_job=broken", "LABEL_monitor=codelab-monitor", "RECEIVER=default", "STATUS=firing"},
	}
)

func TestAmDataToEnv(t *testing.T) {
	for td, expectedEnv := range amDataToEnvMap {
		env := amDataToEnv(td)
		sort.Sort(sort.StringSlice(env))
		sort.Sort(sort.StringSlice(expectedEnv))

		if !reflect.DeepEqual(env, expectedEnv) {
			log.Fatalf("Expected:\n%#v\nbut got:\n%#v", expectedEnv, env)
		}
	}
}
