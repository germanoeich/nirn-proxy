# Nirn-proxy
Nirn-proxy is a highly available, transparent & dynamic HTTP proxy that 
handles Discord ratelimits for you and exports meaningful prometheus metrics.
This project is at the heart of [Dyno](https://dyno.gg), handling several hundreds of requests per sec across hundreds of bots all while keeping 429s at ~100 per hour.

It is designed to be minimally invasive and exploits common library patterns to make the adoption as simple as a URL change.

#### Features

- Highly available, horizontally scalable
- Transparent ratelimit handling, per-route and global
- Works with any API version (Also supports using two or more versions for the same bot)
- Small resource footprint
- Works with webhooks
- Works with Bearer tokens
- Supports an unlimited number of clients (Bots and Bearer)
- Prometheus metrics exported out of the box
- No hardcoded routes, therefore no need of updates for new routes introduced by Discord

### Usage
Binaries can be found [here](https://github.com/germanoeich/nirn-proxy/releases). Docker images can be found [here](https://github.com/germanoeich/nirn-proxy/pkgs/container/nirn-proxy)

The proxy sits between the client and discord. Instead of pointing to discord.com, you point to whatever IP and port the proxy is running on, so discord.com/api/v9/gateway becomes 10.0.0.1:8080/api/v9/gateway. This can be achieved in many ways, some suggestions are host remapping on the OS level, DNS overrides or changes to the library code. Please note that the proxy currently does not support SSL.

Configuration options are

| Variable        | Value                                       | Default |
|-----------------|---------------------------------------------|---------|
| LOG_LEVEL       | panic, fatal, error, warn, info, debug, trace | info    |
| PORT            | number                                      | 8080    |
| METRICS_PORT    | number                                      | 9000    |
| ENABLE_METRICS  | boolean                                     | true    |
| ENABLE_PPROF    | boolean                                     | false   |
| BUFFER_SIZE     | number                                      | 50      |
| OUTBOUND_IP     | string                                      | ""      |
| BIND_IP         | string                                      | 0.0.0.0 |
| REQUEST_TIMEOUT | number (milliseconds)                       | 5000    |
| CLUSTER_PORT    | number                                      | 7946    |
| CLUSTER_MEMBERS | string list (comma separated)               | ""      |
| CLUSTER_DNS     | string                                      | ""      |
| MAX_BEARER_COUNT| number                                      | 1024    |
| DISABLE_HTTP_2  | bool                                        | true    |
| BOT_RATELIMIT_OVERRIDES | string list (comma separated)       | ""      |
| DISABLE_GLOBAL_RATELIMIT_DETECTION | boolean                  | false   |

Information on each config var can be found [here](https://github.com/germanoeich/nirn-proxy/blob/main/CONFIG.md)

.env files are loaded if present

### Behaviour

The proxy listens on all routes and relays them to Discord, while keeping track of ratelimit buckets and making requests wait if there are no tokens to spare. The proxy fires requests sequentially for each bucket and ordering is preserved. The proxy does not modify the requests in any way so any library compatible with Discords API can be pointed at the proxy and it will not break the library, even with the libraries own ratelimiting intact.

When using the proxy, it is safe to remove the ratelimiting logic from clients and fire requests instantly, however, the proxy does not handle retries. If for some reason (i.e shared ratelimits, internal discord ratelimits, etc) the proxy encounters a 429, it will return that to the client. It is safe to immediately retry requests that return 429 or even setup retry logic elsewhere (like in a load balancer or service mesh).

The proxy also guards against known scenarios that might cause a cloudflare ban, like too many webhook 404s or too many 401s.

### Proxy specific responses

The proxy may return a 408 Request Timeout if Discord takes more than $REQUEST_TIMEOUT milliseconds to respond. This allows you to identify and react to routes that have issues.

Requests may also return a 408 status code in the event that they were aborted because of ratelimits, as documented above.

### Limitations

The ratelimiting only works with `X-RateLimit-Precision` set to `seconds`. If you are using Discord API v8+, that is the only possible behaviour. For users on v6 or v7, please refer to your library docs for information on which precision it uses and how to change it to seconds.

The proxy tries its best to detect your REST global limits, but Discord does not expose this information. Be sure to set `BOT_RATELIMIT_OVERRIDES` for any clients with elevated limits.

### High availability

The proxy can be run in a cluster by setting either `CLUSTER_MEMBERS` or `CLUSTER_DNS` env vars. When in cluster mode, all nodes are a suitable gateway for all requests and the proxy will route requests consistently using the bucket hash.

It's recommended that all nodes are reachable through LAN. Please reach out if a WAN cluster is desired for your use case.

If a node fails, there is a brief period where it will be unhealthy but requests will still be routed to it. When these requests fail, the proxy will mock a 429 to send back to the user. The 429 will signal the client to wait 1s and will have a custom header `generated-by-proxy`. This is done in order to allow seamless retries when a member fails. If you want to backoff, use the custom header to override your lib retry logic.

The cluster uses [SWIM](https://www.cs.cornell.edu/projects/Quicksilver/public_pdfs/SWIM.pdf), which is an [AP protocol](https://en.wikipedia.org/wiki/CAP_theorem) and is powered by hashicorps excellent [memberlist](https://github.com/hashicorp/memberlist) implementation.

Being an AP system means that the cluster will tolerate a network partition and needs no quorum to function. In case a network partition occurs, you'll have two clusters running independently, which may or may not be desirable. Configure your network accordingly.

In case you want to specifically target a node (i.e, for troubleshooting), set the `nirn-routed-to` header on the request. The value doesn't matter. This will prevent the node from routing the request to another node.

During recovery periods or when nodes join/leave the cluster, you might notice increased 429s. This is expected since the hashing table is changing as members change. Once the cluster settles into a stable state, it'll go back to normal.

Global ratelimits are handled by a single node on the cluster, however this affinity is soft. There is no concept of leader or elections and if this node leaves, the cluster will simply pick a new one. This is a bottleneck and might increase tail latency, but the other options were either too complex, required an external storage, or would require quorum for the proxy to function. Webhooks and other requests with no token bypass this mechanism completely.

The best deployment strategy for the cluster is to kill nodes one at a time, preferably with the replacement node already up.

### Bearer Tokens

Bearer tokens are first class citizens. They are treated differently than bot tokens, while bot queues are long lived and never get evicted, Bearer queues are put into an LRU and are spread out by their token hash instead of by the path hash. This provides a more even spread of bearer queues across nodes in the cluster. In addition, Bearer globals are always handled locally. You can control how many bearer queues to keep at any time with the MAX_BEARER_COUNT env var.

### Why?

As projects grow, it's desirable to break them into multiple pieces, each responsible for its own domain. Discord provides gateway sharding on their end but REST can get tricky once you start moving logic out of the shards themselves and lose the guild affinity that shards inherently have, thus a centralized place for handling ratelimits is a must to prevent cloudflare bans and prevent avoidable 429s. At the time this project was created, there was no alternative that fully satisfied our requirements like multi-bot support. We are also early adopters of Discord features, so we need a proxy that supports new routes without us having to manually update it. Thus, this project was born.

### Resource usage

This will vary depending on your usage, how many unique routes you see, etc. For reference, for Dynos use case, doing 150req/s, the usage is ~0.3 CPU and ~550MB of RAM. The proxy can comfortably run on a cheap VPS or an ARM based system.

### Metrics / Health

| Key                                | Labels                                 | Description                                                |
|------------------------------------|----------------------------------------|------------------------------------------------------------|
|nirn_proxy_error                    | none                                   | Counter for errors                                         |
|nirn_proxy_requests                 | method, status, route, clientId        | Histogram that keeps track of all request metrics          |
|nirn_proxy_open_connections         | route, method                          | Gauge for open client connections with the proxy           |
|nirn_proxy_requests_routed_sent     | none                                   | Counter for requests routed to other nodes                 |
|nirn_proxy_requests_routed_received | none                                   | Counter for requests received from other nodes             |
|nirn_proxy_requests_routed_error    | none                                   | Counter for requests routed that failed                    |

Note: 429s can produce two status: 429 Too Many Requests or 429 Shared. The latter is only produced for requests that return with the x-ratelimit-scope header set to "shared", which means they don't count towards the cloudflare firewall limit and thus should not be used for alerts, etc.

The proxy has an internal endpoint located at `/nirn/healthz` for liveliness and readiness probes.

### Profiling

The proxy can be profiled at runtime by enabling the ENABLE_PPROF flag and browsing to `http://ip:7654/debug/pprof/`

### Related projects

[nirn-probe](https://github.com/germanoeich/nirn-probe) - Checks and alerts if a server is cloudflare banned

##### Acknowledgements
- [Eris](https://github.com/abalabahaha/eris) - used as reference throughout this project
- [Twilight](https://github.com/twilight-rs) - used as inspiration and reference
- [@bsian](https://github.com/bsian03) & [@bean](https://github.com/beanjo55) - for listening to my rants and providing assistance