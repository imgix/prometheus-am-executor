#!/bin/bash
set -euo pipefail

readonly ALERT_EXAMPLE='{"receiver":"default","status":"firing","alerts":[{"status":"firing","labels":{"alertname":"InstanceDown","instance":"localhost:1234","job":"broken","monitor":"codelab-monitor"},"annotations":{},"startsAt":"2016-04-07T18:08:52.804+02:00","endsAt":"0001-01-01T00:00:00Z","generatorURL":""}],"groupLabels":{"alertname":"InstanceDown"},"commonLabels":{"alertname":"InstanceDown","instance":"localhost:1234","job":"broken","monitor":"codelab-monitor"},"commonAnnotations":{},"externalURL":"http://oldpad:9093","version":"3","groupKey":9777663806026784477}'

go build

TMPFILE=$(tempfile)

echo "Testing basic command execution"
./prometheus-am-executor bash -c 'echo "$AMX_RECEIVER:$AMX_ALERT_1_START:$AMX_ALERT_1_LABEL_instance"' > "$TMPFILE" 2>&1 &
PID=$!
trap "kill $PID; rm '$TMPFILE'" EXIT
sleep 1

if ! curl --fail -X 'POST' http://localhost:8080 -d "$ALERT_EXAMPLE"; then
  echo "Couldn't post alerts to executor" >&2
  exit 1
fi
sleep 1

  if ! grep -q "bash: default:1460045332:localhost:1234" "$TMPFILE"; then
  echo "Unexpected output:"
  cat "$TMPFILE"
fi
