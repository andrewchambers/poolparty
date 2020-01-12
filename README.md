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
$ poolparty --pool-size 6 -- janet webapp.janet
```

To restart the janet workers:

```
$ poolparty-ctl restart-workers
```

# Building

Parts of poolparty are implemented in go, to build this you need a recent go compiler then run:
```
$ cd cmd/poolparty
$ go build
$ cd ../pool-party-ctl
$ go build
```