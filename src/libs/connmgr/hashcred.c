/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2001 Novell, Inc. All Rights Reserved.
 * 
 * This program is free software; you can redistribute it and/or
 * modify it under the terms of version 2 of the GNU General Public License
 * as published by the Free Software Foundation.
 * 
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 * 
 * You should have received a copy of the GNU General Public License
 * along with this program; if not, contact Novell, Inc.
 * 
 * To contact Novell about this file by physical or electronic mail, you 
 * may find current contact information at www.novell.com.
 * </Novell-copyright>
 ****************************************************************************/

#if 0
#include <config.h>
#include <xpl.h>
#include <msgapi.h>
#include <gatekeeper.h>
#include <gklib.h>

#define ORGANIZATION_BUFFER_SIZE    100

#define GK_CRED_STORE_SIZE        4096
#define GK_CRED_DIGEST_SIZE       16
#define GK_CRED_CHUNK_SIZE        (GK_CRED_STORE_SIZE * GK_CRED_DIGEST_SIZE) / GK_HASH_SIZE

static BOOL
GetOrgName(unsigned char *Organization)
{
    unsigned long len;
    unsigned char *ptr;
    unsigned char *buffer;

    /* get the treename */
    buffer = MemStrdup(MsgGetServerDN(NULL));
    if (buffer) {
        if (buffer[0] == '\\') {
            ptr = strchr(buffer + 1, '\\');
            if (ptr) {
                len = ptr - (buffer + 1);
                if (len > 0) {
                    if (len < ORGANIZATION_BUFFER_SIZE) {
                        *ptr = '\0';
                        strcpy(Organization, buffer + 1);
                            *ptr = '\\';    
                    } else {
                        memcpy(Organization, buffer + 1, ORGANIZATION_BUFFER_SIZE);
                        Organization[ORGANIZATION_BUFFER_SIZE] = '\0';
                    }
                    MemFree(buffer);
                    return(TRUE);
                }
            }
        }

        MemFree(buffer);
    }

    return(FALSE);
}

BOOL
HashCredential(unsigned char *Credential, unsigned char *Hash)
{
    unsigned char organization[ORGANIZATION_BUFFER_SIZE + 1];
    unsigned char *ptr;
    unsigned long organizationLen;
    unsigned long len;

    if (Hash && Credential) {
        len = strlen(Credential);
        if (GetOrgName(organization)) {
            organizationLen    = strlen(organization);
            if (len >= GK_CRED_STORE_SIZE) {
                unsigned long    i;
                MD5_CTX        mdContext;
                unsigned char    digest[GK_CRED_DIGEST_SIZE];
                unsigned char   *srcPtr;
                unsigned char   *dstPtr;
                unsigned char   *dstEnd;
                
                srcPtr = Credential;
                dstPtr = Hash;
                dstEnd = Hash + GK_HASH_SIZE;
                
                MD5_Init(&mdContext);
                MD5_Update(&mdContext, srcPtr,  GK_CRED_CHUNK_SIZE);    
                MD5_Update(&mdContext, organization, organizationLen);
                MD5_Final(digest, &mdContext);

                srcPtr += GK_CRED_CHUNK_SIZE;

                for (i = 0; i < GK_CRED_DIGEST_SIZE; i++) {
                    if (dstPtr < dstEnd) {
                        *dstPtr = digest[i];
                        dstPtr++;
                    }
                }

                 do {
                    MD5_Init(&mdContext);
                    MD5_Update(&mdContext, srcPtr,  GK_CRED_CHUNK_SIZE);
                    MD5_Update(&mdContext, Hash, dstPtr - Hash);
                    MD5_Final(digest, &mdContext);

                    srcPtr += GK_CRED_CHUNK_SIZE;

                    for (i = 0; i < GK_CRED_DIGEST_SIZE; i++) {
                        if (dstPtr < dstEnd) {
                            *dstPtr = digest[i];
                            dstPtr++;
                        }
                    }
                } while (dstPtr < dstEnd);

                /* Hash now contains a non-terminated 128 byte octet string */
                return(TRUE);
            }

            XplConsolePrintf("\rGate Keeper credentials are not properly configured!\r\n");
            return(FALSE);
        }

        XplConsolePrintf("\Bongo can not determine what organization it is running on.\r\n");
        return(FALSE);
    }
    return(FALSE);
}
#endif
