#define _POSIX_SOURCE
#include <janet.h>
#include <stdio.h>

static Janet out_fdopen(int32_t argc, Janet *argv) {
    janet_fixarity(argc, 1);
    const int fd = janet_getinteger(argv, 0);
#ifdef JANET_WINDOWS
#define fdopen _fdopen
#endif
    FILE *f = fdopen(fd, "wb");
    return f ? 
      janet_makefile(f, JANET_FILE_WRITE|JANET_FILE_BINARY) : janet_wrap_nil();
}

static const JanetReg cfuns[] = {
    {"out-fdopen", out_fdopen, NULL},
    {NULL, NULL, NULL}};

JANET_MODULE_ENTRY(JanetTable *env) { janet_cfuns(env, "_poolparty", cfuns); }
