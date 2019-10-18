follow the steps as mentioned in https://github.com/asking28/resource-monitoring/wiki/Prometheus-am-executor

simply git clone and do a "go build"

if you get this error- 
/prometheus/client_golang/prometheus/desc.go:22:2: cannot find package "github.com/cespare/xxhash/v2" in any of


modify /prometheus/client_golang/prometheus/desc.go and remove v2 from the import 

so the package in the  /prometheus/client_golang/prometheus/desc.go should be "github.com/cespare/xxhash"

you might need to modify other files and correct the package import
