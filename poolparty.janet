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
  (def buf @"")
  (while true
    (buffer/clear buf)
    (file/read inf :line buf)
    (when (empty? buf) (break))
    (def req (json/decode buf false true))
    (unless (table? req) (error "malformed request"))
    (fixup-request req)
    (def resp (handler req))
    # Reuse buffer
    (buffer/clear buf)
    (json/encode resp "" "" buf)
    (buffer/push-byte buf (comptime ("\n" 0)))
    (file/write outf buf)
    (file/flush outf)))
