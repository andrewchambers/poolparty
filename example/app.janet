(import uri)
(import poolparty)

# An example main.
(defn main [&]
  (setdyn :out stderr)

  (poolparty/serve
    (fn [req]
      (def resp @"")
      (with-dyns [:out resp]
        (printf "Request:\n%p" req)
        (printf "Parsed request uri:\n%p" (uri/parse (req :uri))))
      @{:status 200
        :headers { "Content-Type" "text/plain; charset=utf-8" }
        :body resp})))