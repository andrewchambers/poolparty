(declare-project
  :name "poolparty"
  :author "Andrew Chambers"
  :license "MIT"
  :url "https://github.com/andrewchambers/poolparty"
  :repo "git+https://github.com/andrewchambers/poolparty")

(def poolparty-src [
    "cmd/poolparty/main.go"
    "cmd/poolparty-ctl/main.go"
    "poolparty.go"
    "ctl.go"
    "go.mod"
])

(add-dep "build" "build/poolparty")
(add-dep "build" "build/poolparty-ctl")

(rule "build/poolparty" poolparty-src
  (shell "go" "build" "-mod=vendor" "-o" "./build/poolparty" "./cmd/poolparty/main.go"))

(rule "build/poolparty-ctl" poolparty-src
  (shell "go" "build" "-mod=vendor" "-o" "./build/poolparty-ctl" "./cmd/poolparty-ctl/main.go"))

(declare-source
  :source @["poolparty.janet"])

(declare-native
  :name "_poolparty"
  :source @["csrc/poolparty.c"])

(install-rule "build/poolparty" (dyn :binpath JANET_BINPATH))
(install-rule "build/poolparty-ctl" (dyn :binpath JANET_BINPATH))
