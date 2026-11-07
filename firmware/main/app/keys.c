/* Key fingerprints, as OpenSSH prints them: "SHA256:" and the unpadded base64 of the SHA-256 of the key's blob
 * (parent 2.5, 13.8: shown in full). Hashing and base64 are mbedtls's; nothing here is our own cryptography. */
#include <string.h>

#include "app_internal.h"
#include "mbedtls/base64.h"
#include "mbedtls/sha256.h"

bool app_fingerprint(const char *key, char *out, size_t cap)
{
    const char *b64 = strchr(key, ' ');
    if (!b64) {
        return false;
    }
    b64++;
    size_t b64_len = strcspn(b64, " "); /* any comment after the blob isn't part of it */
    unsigned char blob[APP_KEY_MAX];
    size_t blob_len;
    if (mbedtls_base64_decode(blob, sizeof(blob), &blob_len, (const unsigned char *)b64, b64_len) != 0) {
        return false;
    }
    unsigned char digest[32];
    if (mbedtls_sha256(blob, blob_len, digest, 0) != 0) {
        return false;
    }
    unsigned char enc[48];
    size_t enc_len;
    if (mbedtls_base64_encode(enc, sizeof(enc), &enc_len, digest, sizeof(digest)) != 0) {
        return false;
    }
    while (enc_len > 0 && enc[enc_len - 1] == '=') {
        enc_len--;
    }
    if (cap < 7 + enc_len + 1) {
        return false;
    }
    memcpy(out, "SHA256:", 7);
    memcpy(out + 7, enc, enc_len);
    out[7 + enc_len] = '\0';
    return true;
}
