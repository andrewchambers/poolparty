# poolparty

An http server for janet that creates a pool of janet vm's and dispatches requests to them.

# Quick example

webapp.janet
```
(import poolparty)

(defn handler [req]
  @{:status 200
    :body "ok!"})

(defn main [&]
  (poolparty/serve stdin stdout handler))
```

Then launch pool party from the command line:

```
$ poolparty --pool-size 6 --static-files ./static -- janet webapp.janet
```
