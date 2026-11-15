//go:build darwin && cgo

package secretstore

/*
#cgo LDFLAGS: -framework CoreFoundation -framework Security
#include <stdlib.h>
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>

static CFStringRef eggify_str(const char *s) {
	return CFStringCreateWithCString(NULL, s, kCFStringEncodingUTF8);
}

static CFMutableDictionaryRef eggify_query(const char *svc, const char *acct) {
	CFMutableDictionaryRef q = CFDictionaryCreateMutable(NULL, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFStringRef s = eggify_str(svc), a = eggify_str(acct);
	CFDictionarySetValue(q, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(q, kSecAttrService, s);
	CFDictionarySetValue(q, kSecAttrAccount, a);
	CFRelease(s);
	CFRelease(a);
	return q;
}

// Items go to the login (file-based) keychain on purpose: its ACL trusts only
// the creating application, while the data-protection keychain would need
// keychain-access-group entitlements.
static OSStatus eggify_kc_get(const char *svc, const char *acct, CFDataRef *out) {
	CFMutableDictionaryRef q = eggify_query(svc, acct);
	CFDictionarySetValue(q, kSecReturnData, kCFBooleanTrue);
	CFDictionarySetValue(q, kSecMatchLimit, kSecMatchLimitOne);
	OSStatus st = SecItemCopyMatching(q, (CFTypeRef *)out);
	CFRelease(q);
	return st;
}

static OSStatus eggify_kc_set(const char *svc, const char *acct, const char *label, const void *data, long n) {
	CFDataRef d = CFDataCreate(NULL, data, n);
	CFMutableDictionaryRef q = eggify_query(svc, acct);
	CFMutableDictionaryRef upd = CFDictionaryCreateMutable(NULL, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFDictionarySetValue(upd, kSecValueData, d);
	OSStatus st = SecItemUpdate(q, upd);
	if (st == errSecItemNotFound) {
		CFStringRef l = eggify_str(label);
		CFDictionarySetValue(q, kSecAttrLabel, l);
		CFDictionarySetValue(q, kSecValueData, d);
		st = SecItemAdd(q, NULL);
		CFRelease(l);
	}
	CFRelease(upd);
	CFRelease(q);
	CFRelease(d);
	return st;
}

static OSStatus eggify_kc_delete(const char *svc, const char *acct) {
	CFMutableDictionaryRef q = eggify_query(svc, acct);
	OSStatus st = SecItemDelete(q);
	CFRelease(q);
	return st;
}

static char *eggify_err(OSStatus st) {
	CFStringRef s = SecCopyErrorMessageString(st, NULL);
	if (s == NULL) return NULL;
	CFIndex n = CFStringGetMaximumSizeForEncoding(CFStringGetLength(s), kCFStringEncodingUTF8) + 1;
	char *buf = malloc(n);
	if (buf != NULL && !CFStringGetCString(s, buf, n, kCFStringEncodingUTF8)) {
		free(buf);
		buf = NULL;
	}
	CFRelease(s);
	return buf;
}

static int eggify_ent_true(CFDictionaryRef ents, const char *name) {
	if (ents == NULL) return 0;
	CFStringRef k = eggify_str(name);
	CFTypeRef v = CFDictionaryGetValue(ents, k);
	CFRelease(k);
	return v != NULL && CFGetTypeID(v) == CFBooleanGetTypeID() && CFBooleanGetValue((CFBooleanRef)v);
}

// eggify_signing reports the running binary's team identifier (copied into team,
// empty when ad-hoc/unsigned), code-signing flags, and whether it carries an
// entitlement that re-opens code injection (weak = 1).
static int eggify_signing(char *team, int teamLen, unsigned int *flags, int *weak) {
	SecCodeRef code = NULL;
	CFDictionaryRef info = NULL;
	team[0] = 0;
	*flags = 0;
	*weak = 0;
	if (SecCodeCopySelf(kSecCSDefaultFlags, &code) != errSecSuccess) return -1;
	OSStatus st = SecCodeCopySigningInformation((SecStaticCodeRef)code,
		kSecCSSigningInformation | kSecCSDynamicInformation | kSecCSRequirementInformation, &info);
	CFRelease(code);
	if (st != errSecSuccess || info == NULL) return -1;
	CFStringRef t = CFDictionaryGetValue(info, kSecCodeInfoTeamIdentifier);
	if (t != NULL) CFStringGetCString(t, team, teamLen, kCFStringEncodingUTF8);
	CFNumberRef f = CFDictionaryGetValue(info, kSecCodeInfoFlags);
	if (f != NULL) CFNumberGetValue(f, kCFNumberSInt32Type, flags);
	CFDictionaryRef ents = CFDictionaryGetValue(info, kSecCodeInfoEntitlementsDict);
	*weak = eggify_ent_true(ents, "com.apple.security.cs.allow-dyld-environment-variables") ||
		eggify_ent_true(ents, "com.apple.security.cs.disable-library-validation") ||
		eggify_ent_true(ents, "com.apple.security.get-task-allow");
	CFRelease(info);
	return 0;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// KeychainService is the service name of Eggify's keychain items.
const KeychainService = "Eggify"

const codeSignatureRuntime = 0x10000 // kSecCodeSignatureRuntime (hardened runtime)

type darwinKeychain struct{}

func newPlatformKeychain() (Backend, func() (bool, string)) {
	return darwinKeychain{}, signingProtects
}

func (darwinKeychain) Name() string { return "keychain" }

func osErr(op string, st C.OSStatus) error {
	msg := ""
	if c := C.eggify_err(st); c != nil {
		msg = C.GoString(c)
		C.free(unsafe.Pointer(c))
	}
	switch st {
	case C.errSecInteractionNotAllowed:
		msg += " (no GUI session or the keychain is locked)"
	case C.errSecUserCanceled, C.errSecAuthFailed:
		msg += " (access was denied at the keychain prompt)"
	}
	return fmt.Errorf("keychain %s: OSStatus %d: %s", op, int(st), msg)
}

func (darwinKeychain) Get(account string) ([]byte, error) {
	svc, acct := C.CString(KeychainService), C.CString(account)
	defer C.free(unsafe.Pointer(svc))
	defer C.free(unsafe.Pointer(acct))
	var data C.CFDataRef
	st := C.eggify_kc_get(svc, acct, &data)
	if st == C.errSecItemNotFound {
		return nil, ErrNotFound
	}
	if st != C.errSecSuccess {
		return nil, osErr("read", st)
	}
	defer C.CFRelease(C.CFTypeRef(data))
	n := C.CFDataGetLength(data)
	return C.GoBytes(unsafe.Pointer(C.CFDataGetBytePtr(data)), C.int(n)), nil
}

func (darwinKeychain) Set(account string, value []byte) error {
	if len(value) == 0 {
		return fmt.Errorf("keychain write: empty value")
	}
	svc, acct := C.CString(KeychainService), C.CString(account)
	label := C.CString("Eggify secret (" + account + ")")
	defer C.free(unsafe.Pointer(svc))
	defer C.free(unsafe.Pointer(acct))
	defer C.free(unsafe.Pointer(label))
	buf := C.CBytes(value)
	defer C.free(buf)
	if st := C.eggify_kc_set(svc, acct, label, buf, C.long(len(value))); st != C.errSecSuccess {
		return osErr("write", st)
	}
	return nil
}

func (darwinKeychain) Delete(account string) error {
	svc, acct := C.CString(KeychainService), C.CString(account)
	defer C.free(unsafe.Pointer(svc))
	defer C.free(unsafe.Pointer(acct))
	st := C.eggify_kc_delete(svc, acct)
	if st == C.errSecSuccess || st == C.errSecItemNotFound {
		return nil
	}
	return osErr("delete", st)
}

// signingProtects reports whether a keychain item created by this binary is
// protected from other same-user code: the ACL must be bound to a stable
// Developer ID identity (team ID) and the hardened runtime must block
// DYLD_INSERT_LIBRARIES/debugger injection into this binary.
func signingProtects() (bool, string) {
	var team [64]C.char
	var flags C.uint
	var weak C.int
	if C.eggify_signing(&team[0], C.int(len(team)), &flags, &weak) != 0 {
		return false, "could not read this binary's code signature"
	}
	teamID := C.GoString(&team[0])
	if teamID == "" {
		return false, "binary is ad-hoc signed or unsigned (keychain access would be tied to this exact build and re-prompt after every update)"
	}
	if uint32(flags)&codeSignatureRuntime == 0 {
		return false, "binary is signed by team " + teamID + " but without the hardened runtime (code injection into it is possible)"
	}
	if weak != 0 {
		return false, "binary is signed by team " + teamID + " but has an entitlement that allows code injection or debugging"
	}
	return true, "binary is signed by team " + teamID + " with the hardened runtime"
}
