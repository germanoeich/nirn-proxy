# Config
All variables are optional unless stated otherwise

##### LOG_LEVEL
Logrus log level. Passed directly to [ParseLevel](https://github.com/sirupsen/logrus/blob/master/logrus.go#L25-L45)

##### PORT
The port to listen for requests on

##### METRICS_PORT 
The port to listen for metrics requests on

##### ENABLE_METRICS
Toggle to enable and register metrics. Disabling may improve resource usage

##### ENABLE_PPROF
Enables the performance profiling handler. Read more [here](https://github.com/google/pprof/blob/master/doc/README.md)

##### BUFFER_SIZE
Size for the internal proxy go channels. Channels are used to synchronize and order requests. As each request comes in, it gets pushed to a channel. In go, channels can be buffered, this var defines the size of this buffer.
Decreasing this will improve memory usage, but beware that once a channel buffer is full, requests will fight to be added to the channel on the next free spot. This means that during high usage periods, a part of the requests will be unordered if this value is set too low.

##### OUTBOUND_IP
The local address to use when firing requests to discord.

Example: `120.121.122.123`

##### BIND_IP
The IP to bind the HTTP server on (both for requests and metrics). 127.0.0.1 will only allow requests coming from the loopback interface. Useful for preventing the proxy from being accessed from outside of LAN, for example.

Example: `10.0.0.42` - Would only listen on LAN

##### REQUEST_TIMEOUT
Defines the amount of time the proxy will wait for a response from discord. Does not include time waiting for ratelimits to clear.

##### CLUSTER_PORT
Sets the port that's going to be used to communicate with other cluster members. Default 7946

##### CLUSTER_MEMBERS
Comma separated list of stable/known members of the cluster. Does not need to include all members, a gossip protocol is used for discovery. You may include a port along with the address and if you don't, CLUSTER_PORT is used. This variable overrides CLUSTER_DNS.

Example: `10.0.0.2,10.0.0.3:7244`

##### CLUSTER_DNS
DNS address that will resolve to multiple members of the cluster. Does not need to include all members, a gossip protocol is used for discovery. While this is the recommended method of discovery for Kubernetes or similar, it does come with a limitation, which is that all nodes must use the same port for communication since DNS can't return port information. The port used by the proxy for requests is broadcasted automatically and doesn't need to be the same for nodes.

If using Kubernetes, create a headless service and use it here for easy clustering.

Example: `nirn-headless.default.svc.cluster.local` or `nirn.mydomain.com`

##### MAX_BEARER_COUNT
Bearer token queues max size. Internally, bearer queues are put in an LRU map, this env var represents the max amount of items for this map.
Requests are never interrupted midway, even when an entry is evicted. A low LRU size may cause increased 429s if a bearer token has too many requests queued and fires another one after eviction.
Default: 1024

## Unstable env vars
Collection of env vars that may be removed at any time, mainly used for Discord introducing new behaviour on their edge api versions

##### DISABLE_401_LOCK
The proxy locks its queue permanently in case a 401 is encountered during normal operation. This env disables this mechanism but not the logging for it.
