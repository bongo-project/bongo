#include <config.h>
#include <xpl.h>
#include <bongoutil.h>

#include <memmgr.h>

#include <nmlib.h>
#include <msgapi.h>
#include <nmap.h>

const int debugging = 0;

/**
 * This program helps to compute the md5 hash to pass in for a system
 * auth against the store.  It is only meant for testing use, not for
 * use in new applications. Most of this code was copied or based on
 * existing code in nmap.c or imapd.c.
 *
 * In order for this program to work, you must have installed and setup bongo.
 */

int
main (int argc, char** argv) {
    XplInit();
    if (argc != 2) {
        XplConsolePrintf("Usage: ./mkauthhash.c <salt>\n");
        return 1;
    } else if (debugging) {
        XplConsolePrintf("Good job, you passed the salt %s\n", argv[1]);
    }
    {
        unsigned char access[NMAP_HASH_SIZE]; // from libs/nmap/nmap.c

	if (!MsgGetServerCredential(access)) {
		XplConsolePrintf("HashCredential failed\n");
		return 6;
	}

        // mix access with salt
        XplHashNew(&ctx, XPLHASH_MD5);
        XplHashWrite(&ctx, argv[1], strlen(argv[1]));
        XplHashWrite(&ctx, access, NMAP_HASH_SIZE);
        XplHashFinal(&ctx, XPLHASH_LOWERCASE, message, XPLHASH_MD5_LENGTH);
        XplConsolePrintf("%s\n", message);
    }
    return 0;
}

