(import json)

(defn- keys-to-keywords
  [req & keys]
  (each k keys
    (put req (keyword k) (get req k))
    (put req k nil))
  req)

(defn- fixup-request
  [req & keys]
  (keys-to-keywords req 
    "file" "headers" "method" "uri" "body"))

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
    (unless (table? req) (error "malformed request"))
    # XXX Once we have a protocol that supports janets 
    # on both sides, we can skip this ugly hack. 
    (fixup-request req)
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