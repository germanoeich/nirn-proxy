# Config
##### LOG_LEVEL
Logrus log level. Passed directly to [ParseLevel](https://github.com/sirupsen/logrus/blob/master/logrus.go#L25-L45)

##### PORT
The port to listen for requests on

##### METRICS_PORT 
The port for to listen on for metrics

##### ENABLE_METRICS
Wether to enable and register metrics. Disabling may improve resource usage

##### ENABLE_PPROF
Enables the performance profiling handler. Read more [here](https://github.com/google/pprof/blob/master/doc/README.md)

##### BUFFER_SIZE
Size for the internal proxy go channels. Channels are used to synchronize and order requests. As each request comes in, it gets pushed to a channel. In go, channels can be buffered, this var defines the size of this buffer.
Decreasing this will improve memory usage, but beware that once a channel buffer is full, requests will fight to be added to the channel on the next free spot. This means that during high usage periods, a part of the requests will be unordered if this value is set too low.

##### OUTBOUND_IP
The local address to use when firing requests to discord.

Example: `"120.121.122.123"`

##### BIND_IP
The IP to bind the HTTP server on (both for requests and metrics). 127.0.0.1 will only allow requests coming from the loopback interface. Useful for preventing the proxy from being accessed from outside of LAN, for example.

Example: `"10.0.0.42"` - Would only listen on LAN

##### REQUEST_TIMEOUT
Defines the amount of time the proxy will wait for a response from discord. Does not include time waiting for ratelimits to clear.