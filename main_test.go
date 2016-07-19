package main

import (
	"log"
	"sort"
	"testing"
	"time"

	"github.com/juju/testing/checkers"
	"github.com/prometheus/alertmanager/template"
)

var (
	amDataToEnvMap = map[*template.Data][]string{
		&template.Data{
			Receiver: "default", Status: "firing", Alerts: template.Alerts{
				template.Alert{Status: "firing", Labels: template.KV{
					"job":       "broken",
					"monitor":   "codelab-monitor",
					"alertname": "InstanceDown",
					"instance":  "localhost:1234",
				},
					Annotations:  template.KV{},
					StartsAt:     time.Unix(1460045332, 0),
					EndsAt:       time.Time{},
					GeneratorURL: "http://oldpad:9090/graph#%5B%7B%22expr%22%3A%22up%20%3D%3D%200%22%2C%22tab%22%3A0%7D%5D",
				},
				template.Alert{Status: "firing", Labels: template.KV{
					"job":       "broken",
					"monitor":   "codelab-monitor",
					"alertname": "InstanceDown",
					"instance":  "localhost:5678",
				},
					Annotations:  template.KV{},
					StartsAt:     time.Unix(1460045332, 0),
					EndsAt:       time.Time{},
					GeneratorURL: "http://oldpad:9090/graph#%5B%7B%22expr%22%3A%22up%20%3D%3D%200%22%2C%22tab%22%3A0%7D%5D",
				},
			},
			GroupLabels: template.KV{"alertname": "InstanceDown"},
			CommonLabels: template.KV{
				"alertname": "InstanceDown",
				"instance":  "localhost:1234",
				"job":       "broken",
				"monitor":   "codelab-monitor",
			},
			CommonAnnotations: template.KV{},
			ExternalURL:       "http://oldpad:9093",
		}: []string{
			"AMX_ALERT_1_END=0",
			"AMX_ALERT_1_LABEL_alertname=InstanceDown",
			"AMX_ALERT_1_LABEL_instance=localhost:1234",
			"AMX_ALERT_1_LABEL_job=broken",
			"AMX_ALERT_1_LABEL_monitor=codelab-monitor",
			"AMX_ALERT_1_START=1460045332",
			"AMX_ALERT_1_STATUS=firing",
			"AMX_ALERT_1_URL=http://oldpad:9090/graph#%5B%7B%22expr%22%3A%22up%20%3D%3D%200%22%2C%22tab%22%3A0%7D%5D",

			"AMX_ALERT_2_END=0",
			"AMX_ALERT_2_LABEL_alertname=InstanceDown",
			"AMX_ALERT_2_LABEL_instance=localhost:5678",
			"AMX_ALERT_2_LABEL_job=broken",
			"AMX_ALERT_2_LABEL_monitor=codelab-monitor",
			"AMX_ALERT_2_START=1460045332",
			"AMX_ALERT_2_STATUS=firing",
			"AMX_ALERT_2_URL=http://oldpad:9090/graph#%5B%7B%22expr%22%3A%22up%20%3D%3D%200%22%2C%22tab%22%3A0%7D%5D",

			"AMX_ALERT_LEN=2",
			"AMX_EXTERNAL_URL=http://oldpad:9093",
			"AMX_GLABEL_alertname=InstanceDown",
			"AMX_LABEL_alertname=InstanceDown",
			"AMX_LABEL_instance=localhost:1234",
			"AMX_LABEL_job=broken",
			"AMX_LABEL_monitor=codelab-monitor",
			"AMX_RECEIVER=default",
			"AMX_STATUS=firing"},
	}
)

func TestAmDataToEnv(t *testing.T) {
	for td, expectedEnv := range amDataToEnvMap {
		env := amDataToEnv(td)
		sort.Sort(sort.StringSlice(env))
		sort.Sort(sort.StringSlice(expectedEnv))

		if ok, err := checkers.DeepEqual(env, expectedEnv); !ok {
			log.Fatal(err)
		}
	}
}
