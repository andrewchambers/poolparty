# poolparty

An http server for janet that creates a pool of janet instances and dispatches requests to them.


# Quick example

webapp.janet
```
(import poolparty)

(defn handler [req]
  @{:status 200
    :body "ok!"})

(defn main [&]
  (poolparty/serve handler))
```

Then launch pool party from the command line:

```
$ poolparty --pool-size 6 -- janet ./example/app.janet
```

To restart the janet workers:

```
$ poolparty-ctl restart-workers
```

# Building

Parts of poolparty are implemented in go, to build this you need a recent go compiler, jpm knows how to invoke go:
```
$ cd poolparty
$ jpm --verbose install
...
```

# Poolparty <-> Worker protocol

Poolparty communicates requests with workers one at a time, a request is first written to the worker's STDIN and once that request is handled, the workers response is written to the file descriptor 3 (chosen to separate it from application logging to stderr or stdout).

Pool party workers request and response packets follow a simple length prefix format:

```
size: int32 # little endian
payload: [size]data # simple byte format
```

In this format the request/response payload is encoded following this [BARE](https://baremessages.org) schema:

```

type HTTPRequest {
  remote_address: string
  uri: string
  method: string
  headers: map[string]string
  body: data
}

type Request = HTTPRequest | ... Reserved


type HTTPResponse {
  status: uint
  headers: map[string][]string
  body: data
}

type Response = HTTPResponse | ... Reserved

```

