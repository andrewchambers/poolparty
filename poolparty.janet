(import _poolparty)

(defn serve
  [handler &keys {:inf inf :outf outf :health-check health-check}]
  (default inf stdin)
  # By default we pass in an extra file descriptor
  # that janet doesn't know about, we open this manually.
  (default outf (_poolparty/out-fdopen 3))
  (when (nil? outf)
    (error "unable to open output fd, this is fd 3 by default."))
  (when (= outf (dyn :out))
    (error "server outf should not be the same as :out, hint: (setdyn :out stderr)"))
  (default health-check (fn [] nil))
  (def buf @"")
  (while true
    (def req (_poolparty/read-request inf))
    (when (= req :health-check)
      (health-check))
    (def resp (handler req))
    (_poolparty/format-response resp buf)
    (file/write outf buf)
    (file/flush outf)
    # Clear buffer if its a large response
    (when (> (length buf) 1000000)
      (buffer/clear buf)
      (buffer/trim buf))))
