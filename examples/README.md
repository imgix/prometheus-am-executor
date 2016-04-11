# prometheus-am-executor examples
## Reboot systems with errors
Sometimes a system might exhibit errors that require a hard reboot. This is an
example on how to use Prometheus and prometheus-am-executor to reboot a machine
a machine based on a alert while making sure enough instances are in service
all the time.

Where in general errors are best exposed as a counter, in this specific case
it's simpler to use a gauge since we can set that back to 0 after the reboot.

Let assume the metric `host_require_reboot` should trigger a reboot if set 1.
To make sure enough instances are in service all the time, the only reboot
should only get triggered if at least 80% of all instances are reachable in the
load balancer. A alerting expression would look like this:

```
ALERT RebootMachine IF
	host_require_reboot == 1 AND
	avg by(backend) (haproxy_server_up{backend="app"}) > 0.8
```

This will trigger an alert `RebootMachine` if `host_require_reboot` equals 1
and there are at least 80% of all servers for backend `app` up.

Now the alert needs to get routed to prometheus-am-executor like in this 
[alertmanager config](alertmanager.conf) example.

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

Since the alert gets triggered if the counter increased in the last 15 minutes,
the alert resolves after 15 minutes without counter increase, so it's important
that the alert gets processed in those 15 minutes or the system won't get
rebooted.
