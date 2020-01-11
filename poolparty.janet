(import json)

(defn serve
  [handler &opt inf outf]
  (setdyn :out stderr)
  (default inf stdin)
  (default outf stdout)
  (def p (parser/new))
  (def inbuf (buffer/new 0))
  (while true
    (buffer/clear inbuf)
    (file/read inf :line inbuf)
    (when (empty? inbuf) (break))
    (def req (json/decode inbuf))
    (unless req (error "malformed request"))
    (def resp (handler req))
    # XXX It would be nice if the encode api would let us reuse the buffer
    (def respb (json/encode resp))
    (buffer/push-byte respb (comptime ("\n" 0)))
    (file/write outf respb)
    (file/flush outf)))

(defn main [&]
  (serve
    (fn [r]
      @{:status 200
        :headers { "Content-Type" "text/plain; charset=utf-8" }
        :body "The pool is ready!"})))