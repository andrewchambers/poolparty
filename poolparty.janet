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
    "file" "headers" "method"
    "uri" "body" "remote-address"
    "poolparty-request-id"))

(defn serve
  [handler &opt inf outf]
  (default inf stdin)
  # By default we pass in an extra file descriptor
  # that janet doesn't know about, we open this manually.
  (default outf (file/fdopen 3 :wb))
  (when (nil? outf)
    (error "unable to open output fd, this is fd 3 by default."))
  (when (= outf (dyn :out))
    (error "server outf should not be the same as :out, hint: (setdyn :out stderr)"))
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
