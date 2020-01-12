(import logfmt)
(import poolparty)

# An example main.
(defn main [&]
  (poolparty/serve
    (fn [r]
      (logfmt/log :msg "new request" :method (r :method) :uri (r :uri))
      @{:status 200
        :headers { "Content-Type" "text/plain; charset=utf-8" }
        :body "The pool is ready!"})))