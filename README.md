# prometheus-am-executor
The prometheus-am-executor is a HTTP server that receives alerts from the
[Prometheus Alertmanager](https://prometheus.io/docs/alerting/alertmanager/) and
executes a given command with alert details set as environment variables.

## Usage
```
Usage: ./prometheus-am-executor [options] script [args..]

  -l string
    	HTTP Port to listen on (default ":8080")
  -v	Enable verbose/debug logging
```

The executor runs the provided script with the following environment variables
set:

- `AMX_RECEIVER`: name of receiver in the AM triggering the alert
- `AMX_STATUS`: alert status
- `AMX_EXTERNAL_URL`: URL to reach alertmanager
- `AMX_ALERT_LEN`: Number of alerts; for iterating through `AMX_ALERT_<n>..` vars
- `AMX_LABEL_<label>`: alert label pairs
- `AMX_GLABEL_<label>`: label pairs used to group alert
- `AMX_ANNOTATION_<key>`: alert annotation key/value pairs
- `AMX_ALERT_<n>_STATUS`: status of alert
- `AMX_ALERT_<n>_START`: start of alert in seconds since epoch
- `AMX_ALERT_<n>_END`: end of alert, 0 for firing alerts
- `AMX_ALERT_<n>_URL`: URL to metric in prometheus
- `AMX_ALERT_<n>_LABEL_<label>`: <value> alert label pairs
- `AMX_ALERT_<n>_ANNOTATION_<key>`: <value> alert annotation key/value pairs

## Example: Reboot systems with errors
Sometimes a system might exhibit errors that require a hard reboot. This is an
example on how to use Prometheus and prometheus-am-executor to reboot a machine
a machine based on a alert while making sure enough instances are in service
all the time.

Let assume the counter `app_errors_unrecoverable_total` should trigger a reboot
if increased by 1. To make sure enough instances are in service all the time,
the reboot should only get triggered if at least 80% of all instances are
reachable in the load balancer. A alerting expression would look like this:

```
ALERT RebootMachine IF
	increase(app_errors_unrecoverable_total[15m]) > 0 AND
	avg by(backend) (haproxy_server_up{backend="app"}) > 0.8
```

This will trigger an alert `RebootMachine` if `app_errors_unrecoverable_total`
increased in the last 15 minutes and there are at least 80% of all servers for
backend `app` up.

Now the alert needs to get routed to prometheus-am-executor like in this 
[alertmanager config](examples/alertmanager.conf) example.

Finally prometheus-am-executor needs to be pointed to a reboot script:

```
./prometheus-am-executor examples/reboot
```

As soon as the counter increases by 1, an alert gets triggered and the
alertmanager routes the alert to prometheus-am-executor which executes the
reboot script.

### Caveats
To make sure a system doesn't get rebooted multiple times, the 
`repeat_interval` needs to be longer than interval used for `increase()`. As
long as that's the case, prometheus-am-executor will run the provided script
only once.

`increase(app_errors_unrecoverable_total[15m])` takes the value of
`app_errors_unrecoverable_total` 15 minutes ago to calculate the increase, it's
required that the metric already exists *before* the counter increase happens.
The Prometheus client library sets counters to 0 by default, but only for
metrics without dynamic labels. Otherwise the metric only appears the first time
it is set. The alert won't get triggered if the metric uses dynamic labels and
was incremented the very first time (the increase from 'unknown to 0). Therefor
you **need** to initialize all error counters with 0.

Since the alert gets triggered if the counter increased in the last 15 minutes,
the alert resolves after 15 minutes without counter increase, so it's important
that the alert gets processed in those 15 minutes or the system won't get
rebooted.
