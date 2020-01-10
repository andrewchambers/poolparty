(import json)

(defn serve
  [inf outf handler]
  (def p (parser/new))
  (def inbuf (buffer/new 0))
  (while true
    (buffer/clear inbuf)
    (file/read inf :line inbuf)
    (when (empty? inbuf) (break))
    (def req (json/decode inbuf))
    (unless req (error "malformed request"))
    (def resp (handler req))
    (def respb (json/encode resp))
    (buffer/push-byte respb (comptime ("\n" 0)))
    (file/write outf respb)
    (file/flush outf)))

(defn main [&]
  (serve stdin stdout
    (fn [r]
      @{:status 200 :body "ok!"})))