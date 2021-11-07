# Nirn-proxy
Nirn is a transparent & dynamic HTTP proxy that handles Discord ratelimits for you and exports meaningful prometheus metrics. It is considered beta software but is being used in production by [Dyno](https://dyno.gg) on the scale of hundreds of requests per second.

#### Features
- Transparent ratelimit handling, per-route and global
- Multi-bot support with automatic detection for elevated REST limits (big bot sharding)
- Works with any API version (Also supports using two or more versions for the same bot)
- Small resource footprint
- Works with webhooks
- Prometheus metrics exported out of the box
- No hardcoded routes, therefore no need of updates for new routes introduced by Discord

### Usage
Binaries can be found [here](https://github.com/germanoeich/nirn-proxy/releases). Docker images can be found [here](https://github.com/germanoeich/nirn-proxy/pkgs/container/nirn-proxy)

The proxy sits between the client and discord. Essentially, instead of pointing to discord.com, you point to whatever IP and port the proxy is running on, so discord.com/api/v9/gateway becomes 10.0.0.1:8080/api/v9/gateway. This can be achieved in many ways, some suggestions are host remapping on the OS level, DNS overrides or changes to the library code. Please note that the proxy currently does not support SSL.

Configuration options are

| Variable    | Value  | Default |
|-------------|--------|---------|
| LOG_LEVEL   | panic, fatal, error, warn, info, debug, trace | info |
| PORT        | number | 8080    |
| METRICS_PORT| number | 9000    |
| ENABLE_METRICS| boolean| true   |
| ENABLE_PPROF| boolean| false   |

.env files are loaded if present

### Behaviour

The proxy listens on all routes and relays them to Discord, while keeping track of ratelimit buckets and holding requests if there are no tokens to spare. The proxy fires requests sequentially for each bucket and ordering is preserved. The proxy does not modify the requests in any way so any library compatible with Discords API can be pointed at the proxy and it will not break the library, even with the libraries own ratelimiting intact.

When using the proxy, it is safe to remove the ratelimiting logic from clients and fire requests instantly, however, the proxy does not handle retries. If for some reason (i.e shared ratelimits, internal discord ratelimits, etc) the proxy encounters a 429, it will return that to the client. It is safe to immediately retry requests that return 429 or even setup retry logic elsewhere (like in a load balancer or service mesh).

The proxy also guards against known scenarios that might cause a cloudflare ban, like too webhook 404s or too many 401s.

### Limitations

The ratelimiting only works with `X-RateLimit-Precision` set to `seconds`. If you are using Discord API v8+, that is the only possible behaviour. For users on v6 or v7, please refer to your library docs for information on which precision it uses and how to change it to seconds.

Bearer tokens should work, however this was not at all tested and is not the main use case for this project

### Why?

As projects grow, it's desirable to break them into multiple pieces, each responsible for its own domain. Discord provides gateway sharding on their end but REST can get tricky once you start moving logic out of the shards themselves and lose the guild affinity that shards inherently have, thus a centralized place for handling ratelimits is a must to prevent cloudflare bans and prevent avoidable 429s. At the time this project was created, there was no alternative that fully satisfied our requirements like multi-bot support. We are also early adopters of Discord features, so we need a proxy that supports new routes without us having to manually update it. Thus, this project was born.

### Resource usage

This will vary depending on your usage, how many unique routes you see, etc. For reference, for Dynos use case, doing 150req/s, the usage is ~0.3 CPU and ~550MB of RAM. The proxy can comfortably run on a cheap VPS or an ARM based system.

### Metrics

| Key               | Labels                                 | Description                                    |
|-------------------|----------------------------------------|------------------------------------------------|
|nirn_proxy_error   | none                                   | Counter for errors                             |
|nirn_proxy_requests| method, status, route, clientId        | Summary that keeps track of all request metrics|

### Profiling

The proxy can be profiled at runtime by enabling the ENABLE_PPROF flag and browsing to `http://ip:7654/debug/pprof/`

##### Acknowledgements
- [Eris](https://github.com/abalabahaha/eris) - used as reference throughout this project
- [Twilight](https://github.com/twilight-rs) - used as inspiration and reference
- [@bsian](https://github.com/bsian03) & [@bean](https://github.com/beanjo55) - for listening to my rants and providing assistance