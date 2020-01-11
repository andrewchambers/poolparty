(import json)

(defn serve
  [handler &opt inf outf]
  (setdyn :out stderr)
  (default inf stdin)
  (default outf stdout)
  (def buf @"")
  (while true
    (buffer/clear buf)
    (file/read inf :line buf)
    (when (empty? buf) (break))
    (def req (json/decode buf true true))
    (unless (table? req) (error "malformed request"))
    (def resp (handler req))
    # Reuse buffer
    (buffer/clear buf)
    (json/encode resp "" "" buf)
    (buffer/push-byte buf (comptime ("\n" 0)))
    (file/write outf buf)
    (file/flush outf)))

(defn main [&]
  (serve
    (fn [r]
      @{:status 200
        :headers { "Content-Type" "text/plain; charset=utf-8" }
        :body "The pool is ready!"})))
