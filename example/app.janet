(import poolparty)

# An example main function.
(defn main [&]
  (poolparty/serve
    (fn [req]
      @{:status 200
        :headers 
          {"Content-Type" "text/plain; charset=utf-8"
           "Keep-Alive" "0"}
        :body @"hello world!"})))