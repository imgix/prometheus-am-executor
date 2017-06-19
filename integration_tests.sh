#!/bin/bash
set -euo pipefail
readonly ALERT_EXAMPLE='{"receiver":"default","status":"firing","alerts":[{"status":"firing","labels":{"alertname":"InstanceDown","instance":"localhost:1234","job":"broken","monitor":"codelab-monitor"},"annotations":{},"startsAt":"2016-04-07T18:08:52.804+02:00","endsAt":"0001-01-01T00:00:00Z","generatorURL":""},{"status":"firing","labels":{"alertname":"InstanceDown","instance":"localhost:5678","job":"broken","monitor":"codelab-monitor"},"annotations":{},"startsAt":"2016-04-07T18:08:52.804+02:00","endsAt":"0001-01-01T00:00:00Z","generatorURL":""}],"groupLabels":{"alertname":"InstanceDown"},"commonLabels":{"alertname":"InstanceDown","job":"broken","monitor":"codelab-monitor"},"commonAnnotations":{},"externalURL":"http://oldpad:9093","version":"3","groupKey":9777663806026784477}'

go build

TMPFILE=$(mktemp)

echo "Testing basic command execution, logging to $TMPFILE"
./prometheus-am-executor bash -c 'env' > "$TMPFILE" 2>&1 &
PID=$!
sleep 1
trap "kill $PID; rm '$TMPFILE'" EXIT

if ! curl --fail -X 'POST' http://localhost:8080 -d "$ALERT_EXAMPLE"; then
  echo "Couldn't post alerts to executor" >&2
  exit 1
fi
sleep 1

if ! grep -q "AMX_ALERT_1_START=1460045332" "$TMPFILE"; then
  echo "Unexpected output:"
  cat "$TMPFILE"
  exit 1
fi

if ! grep -q "AMX_ALERT_2_LABEL_instance=localhost:5678" "$TMPFILE"; then
  echo "Unexpected output:"
  cat "$TMPFILE"
  exit 1
fi

echo "Tests successful"
