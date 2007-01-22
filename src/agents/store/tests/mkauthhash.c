#include <config.h>
#include <xpl.h>
#include <bongoutil.h>
#include <gnutls/openssl.h>

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
    if (argc != 2) {
        XplConsolePrintf("Usage: ./mkauthhash.c <salt>\n");
        return 1;
    } else if (debugging) {
        XplConsolePrintf("Good job, you passed the salt %s\n", argv[1]);
    }
    {
        unsigned char access[NMAP_HASH_SIZE]; // from libs/nmap/nmap.c
        MDBHandle     directoryHandle;        // ditto
        MDBValueStruct *v;
        unsigned char digest[16];
        unsigned char message[33];
        MD5_CTX context;
        int i;
        BOOL ccode;

        // Initialize used libraries
        if ( !MemoryManagerOpen("mkauthhash.c")) {
            XplConsolePrintf("MemoryManagerOpen failed\n");
            return 1;
        } else if (debugging) {
            XplConsolePrintf("MemoryManagerOpen succeeded\n");
        }
        if (!MDBInit()) {
            XplConsolePrintf("MDBInit failed\n");
            return 2;
        } else if (debugging) {
            XplConsolePrintf("MDBInit suceeded\n");
        }
        directoryHandle = (MDBHandle) MsgInit();
        if ( !directoryHandle ) {
            XplConsolePrintf("MsgInit failed\n");
            return 3;
        } else if (debugging) {
            XplConsolePrintf("MsgInit succeeded\n");
        }
        // compute the contents of the access field
        v = MDBCreateValueStruct(directoryHandle, NULL);
        if ( !v ) {
            XplConsolePrintf("MDBCreateValueStruct failed\n");
            return 4;
        } else if (debugging) {
            XplConsolePrintf("Successfully created a MDB value struct\n");
        }
        if (debugging) {
            XplConsolePrintf("Retrieving the acl property:\n"
                "\tMSGSRV_ROOT=<%s>\n\tMSGSRV_A_ACL=<%s>\n", MSGSRV_ROOT, MSGSRV_A_ACL);
        }
        MDBRead(MSGSRV_ROOT, MSGSRV_A_ACL, v);
        if ( !v->Used ) {
            XplConsolePrintf("Couldn't retrieve the acl property\n");
            return 5;
        } else if (debugging) {
            XplConsolePrintf("Retrieved the ACL property, %i bytes\n", strlen(v->Value[0]) );
        }
        ccode = HashCredential(MsgGetServerDN(NULL), v->Value[0], access);
        if (!ccode) {
            XplConsolePrintf("HashCredential failed\n");
            return 6;
        } else if (debugging) {
            XplConsolePrintf("HashCredential result stored in access\n");
        }
        MDBDestroyValueStruct(v);

        // mix access with salt
        MD5_Init(&context);
        MD5_Update(&context, argv[1], strlen(argv[1]));
        MD5_Update(&context, access, NMAP_HASH_SIZE);
        MD5_Final(digest, &context);

        // format and print the hash
        for (i = 0; i<16; i++) {
            sprintf(message + i*2, "%02x", digest[i]);
        }
        message[32] = '\0';
        XplConsolePrintf("%s\n", message);
    }
    return 0;
}

