(import _poolparty)

(defn serve
  [handler &opt inf outf]
  (default inf stdin)
  # By default we pass in an extra file descriptor
  # that janet doesn't know about, we open this manually.
  (default outf (_poolparty/out-fdopen 3))
  (when (nil? outf)
    (error "unable to open output fd, this is fd 3 by default."))
  (when (= outf (dyn :out))
    (error "server outf should not be the same as :out, hint: (setdyn :out stderr)"))
  (def buf @"")
  (while true
    (def req (_poolparty/read-request inf))
    (def resp (handler req))
    (_poolparty/format-response resp buf)
    (file/write outf buf)
    (file/flush outf)))
