(import poolparty)

(defn health-check
  []
  #(error "health check failed!")
  (eprint "health check"))

(defn handler [req]
  @{:status 200
    :body "ok!"
    :headers {"Content-Type" "text/plain; charset=utf-8"}})

(defn main [&]
  (poolparty/serve
    handler
    :health-check health-check))