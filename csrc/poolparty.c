#define _POSIX_SOURCE
#include <janet.h>
#include <stdio.h>

static Janet out_fdopen(int32_t argc, Janet *argv) {
    janet_fixarity(argc, 1);
    const int fd = janet_getinteger(argv, 0);
    FILE *f = fdopen(fd, "wb");
    return f ? 
      janet_makefile(f, JANET_FILE_WRITE|JANET_FILE_BINARY) : janet_wrap_nil();
}

static void put_varuint(JanetBuffer *buf, uint64_t x) {
  while (x >= 0x80) {
    janet_buffer_push_u8(buf, (uint8_t)x | 0x80);
    x >>= 7;
  }
  janet_buffer_push_u8(buf, (uint8_t)x);
}

static uint64_t decode_varuint(uint8_t *buf, size_t sz, size_t *offset) {
  int i = 0;
  int s = 0;
  uint64_t v = 0;
  while (1) {
    if (sz < *offset + 1)
      janet_panicf("unexpected end of buffer");
    uint8_t b = buf[*offset];
    *offset += 1;
    if (b < 0x80) {
      if (i > 9 || (i == 9 && b > 1))
        janet_panicf("varuint overflow");
      v |= (uint64_t)b << s;
      break;
    } else {
      v |= ((uint64_t)(b & 0x7f)) << s;
      s += 7;
    }
    i++;
  }
  return v;
}

static Janet decode_string(uint8_t *buf, size_t sz, size_t *offset) {
  size_t slen = decode_varuint(buf, sz, offset);
  if (*offset + slen > sz)
    janet_panic("unable to decode string, input buffer too short");
  Janet s = janet_stringv(buf+*offset, slen);
  *offset += slen;
  return s;
}

static Janet decode_buffer(uint8_t *buf, size_t sz, size_t *offset) {
  size_t blen = decode_varuint(buf, sz, offset);
  if (*offset + blen > sz)
    janet_panic("unable to decode buffer, input buffer too short");
  JanetBuffer *b = janet_buffer(blen);
  janet_buffer_push_bytes(b, buf+*offset, blen);
  *offset += blen;
  return janet_wrap_buffer(b);
}

static Janet read_request(int32_t argc, Janet *argv) {
    janet_fixarity(argc, 1);
    FILE *f = janet_getfile(argv, 0, NULL);

    JanetTable *req = janet_table(8);

    uint8_t szbuf[4];
    if (fread(szbuf, 1, sizeof(szbuf), f) != sizeof(szbuf))
      janet_panic("io error reading packet size");

    uint32_t sz = ( (uint32_t)szbuf[0] << 0 
                  | (uint32_t)szbuf[1] << 8
                  | (uint32_t)szbuf[2] << 16
                  | (uint32_t)szbuf[3] << 24);

    uint8_t *buf = janet_smalloc(sz);

    if (fread(buf, 1, sz, f) != sz)
      janet_panic("io error reading request");

    size_t offset = 0;

    uint64_t variant = decode_varuint(buf, sz, &offset);
    switch (variant) {
      case 0: {
        janet_table_put(req, janet_ckeywordv("remote-address"), decode_string(buf, sz, &offset));
        janet_table_put(req, janet_ckeywordv("uri"), decode_string(buf, sz, &offset));
        janet_table_put(req, janet_ckeywordv("method"), decode_string(buf, sz, &offset));
        uint64_t nheaders = decode_varuint(buf, sz, &offset);
        JanetTable *headers = janet_table(nheaders);
        for (uint64_t i = 0; i < nheaders; i++) {
          Janet k = decode_string(buf, sz, &offset);
          Janet v = decode_string(buf, sz, &offset);
          janet_table_put(headers, k, v);
        }
        janet_table_put(req, janet_ckeywordv("headers"), janet_wrap_table(headers));
        janet_table_put(req, janet_ckeywordv("body"), decode_buffer(buf, sz, &offset));
        break;
      }
      default:
        janet_panicf("unknown or unsupported request variant - %d", variant);
    }

    janet_sfree(buf);
    return janet_wrap_table(req);
}

static Janet format_response(int32_t argc, Janet *argv) {
    janet_fixarity(argc, 2);
    Janet req = argv[0];
    JanetBuffer *buf = janet_getbuffer(argv, 1);


    Janet status = janet_get(req, janet_ckeywordv("status"));
    Janet headers = janet_get(req, janet_ckeywordv("headers"));
    Janet body = janet_get(req, janet_ckeywordv("body"));

    // Reserve enough for the size.
    janet_buffer_setcount(buf, 4);


    put_varuint(buf, 0);
    if janet_checktype(status, JANET_NUMBER) {
      put_varuint(buf, janet_unwrap_number(status));
    } else {
      put_varuint(buf, 200);
    }

    if janet_checktypes(headers, JANET_TFLAG_DICTIONARY) {
      const JanetKV *kv = NULL, *kvs = NULL;
      int32_t len, cap = 0;
      janet_dictionary_view(headers, &kvs, &len, &cap);
      put_varuint(buf, len);

      while ((kv = janet_dictionary_next(kvs, cap, kv))) {
        if (!janet_checktypes(kv->key, JANET_TFLAG_BYTES))
          janet_panicf("header key invalid, got %v", kv->key);

        const uint8_t *kdata;
        int32_t klen;

        janet_bytes_view(kv->key, &kdata, &klen);
        put_varuint(buf, klen);
        janet_buffer_push_bytes(buf, kdata, klen);

        if (janet_checktypes(kv->value, JANET_TFLAG_INDEXED)) {
          const Janet *idata;
          int32_t ilen;
          janet_indexed_view(kv->value, &idata, &ilen);

          put_varuint(buf, ilen);
          for (int32_t i = 0; i < ilen; i++) {
            if (!janet_checktypes(idata[i], JANET_TFLAG_BYTES))
              janet_panicf("header value invalid, got %v", idata[i]);

            const uint8_t *vdata;
            int32_t vlen;
            janet_bytes_view(idata[i], &vdata, &vlen);
            put_varuint(buf, vlen);
            janet_buffer_push_bytes(buf, vdata, vlen);
          }

        } else if (janet_checktypes(kv->value, JANET_TFLAG_BYTES)) {
          const uint8_t *vdata;
          int32_t vlen;
          janet_bytes_view(kv->value, &vdata, &vlen);
          // Buffer of length one.
          put_varuint(buf, 1);
          put_varuint(buf, vlen);
          janet_buffer_push_bytes(buf, vdata, vlen);
        } else {
          janet_panicf("header value invalid, got %v", kv->value);
        }
      }
    } else if (janet_checktype(headers, JANET_NIL)) {
      put_varuint(buf, 0);
    } else {
      janet_panicf("response :headers invalid, got %v", headers);
    }

    if (janet_checktypes(body, JANET_TFLAG_BYTES)) {
       const uint8_t *body_bytes;
       int32_t body_len;
       janet_bytes_view(body, &body_bytes, &body_len);
       put_varuint(buf, body_len); 
       janet_buffer_push_bytes(buf, body_bytes, body_len);
    } else if (janet_checktype(body, JANET_NIL)) {
      put_varuint(buf, 0);
    } else {
      janet_panicf("response :body invalid, got %v", body);
    }

    int32_t rsz = buf->count - 4;
    buf->data[0] = (rsz >> 0) & 0xff;
    buf->data[1] = (rsz >> 8) & 0xff;
    buf->data[2] = (rsz >> 16) & 0xff;
    buf->data[3] = (rsz >> 24) & 0xff;
    return janet_wrap_buffer(buf);
}

static const JanetReg cfuns[] = {
    {"out-fdopen", out_fdopen, NULL},
    {"read-request", read_request, NULL},
    {"format-response", format_response, NULL},
    {NULL, NULL, NULL}};

JANET_MODULE_ENTRY(JanetTable *env) { janet_cfuns(env, "_poolparty", cfuns); }
