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

#include <config.h>
#include <xpl.h>
#include <logger.h>

#include "stored.h"

#if defined(WIN32)
#include <rpcdce.h>

void 
GuidReset(void) 
{
	unsigned long length;
	unsigned char salt[32];
	unsigned char name[MAX_COMPUTERNAME_LENGTH + 1];
	unsigned char *uid = NULL;
	FILETIME date;
	UUID uuid;
	xpl_hash_context context;

	XplHashNew(&context, XPLHASH_SHA1);

	GetSystemTimeAsFileTime(&date);
	sprintf(salt, "%010lu%010lu", date.dwHighDateTime, date.dwLowDateTime);
	XplHashWrite(&context, salt, strlen(salt));

	length = sizeof(name) - 1;
	if (GetComputerName(name, &length)) {
		XplHashWrite(&context, name, strlen(name));
	} else {
		XplRandomData(&name, sizeof(name) - 1);
		XplHashWrite(&context, name, sizeof(name) - 1);
	}
	
	if ((UuidCreate(&uuid) == RPC_S_OK) && 
	    (UuidToString(&uuid, &uid) == RPC_S_OK)) {
		XplHashWrite(&context, uid, strlen(uid));
	} else {
		XplRandomData(&name, sizeof(name) - 1);
		XplHashWrite(&context, name, sizeof(name) - 1);
	}
	if (uid)
		RpcStringFree(&uid);
	
	XplHashFinal(&context, XPLHASH_UPPERCASE, StoreAgent.guid.next,
		XPLHASH_SHA1_LENGTH);
	
	memset(StoreAgent.guid.next + NMAP_GUID_PREFIX_LENGTH, '0',
		NMAP_GUID_SUFFIX_LENGTH);
}
#elif defined(NETWARE) || defined(LIBC)
#error GuidReset not implemented on this platform.

void 
GuidReset(void)
{
    return;
}
#elif defined(LINUX)

#if HAVE_SYS_SYSINFO_H
#include <sys/sysinfo.h>
#endif

void GuidReset(void) {
	char name[256 + 1];
	unsigned char salt[32];
#if HAVE_SYS_SYSINFO_H
	struct sysinfo si;
#endif
	struct timeval tv;
	xpl_hash_context context;
	
	XplHashNew(&context, XPLHASH_SHA1);
	
	gettimeofday(&tv, NULL);
	sprintf(salt, "%010lu%010lu", tv.tv_sec, tv.tv_usec);
	XplHashWrite(&context, salt, strlen(salt));

	if (!gethostname(name, sizeof(name) - 1)) {
		XplHashWrite(&context, name, strlen(name));
	} else {
		XplRandomData(name, sizeof(name) - 1);
		XplHashWrite(&context, name, sizeof(name) - 1);
	}

#if HAVE_SYS_SYSINFO_H
	if (!sysinfo(&si)) {
		XplHashWrite(&context, &si, sizeof(si));
	} else {
		XplRandomData(name, sizeof(name) - 1);
		XplHashWrite(&context, name, sizeof(name) - 1);
	}
#else 
	/* FIXME: should find a sysinfo equiv. for this */
	XplRandomData(name, sizeof(name) - 1);
	XplHashWrite(&context, name, sizeof(name) - 1);
#endif

	XplHashFinal(&context, XPLHASH_UPPERCASE, StoreAgent.guid.next,
		XPLHASH_SHA1_LENGTH);
	
	memset(StoreAgent.guid.next + NMAP_GUID_PREFIX_LENGTH, '0',
		NMAP_GUID_SUFFIX_LENGTH);

	return;
}
#else
#error GuidReset not implemented on this platform.
#endif

void 
GuidAlloc(unsigned char *guid)
{
    int i;
    unsigned char c;
    unsigned char *ptr;

    if (guid) {
        XplWaitOnLocalSemaphore(StoreAgent.guid.semaphore);

        memcpy(guid, StoreAgent.guid.next, NMAP_GUID_LENGTH);

        ptr = StoreAgent.guid.next + NMAP_GUID_PREFIX_LENGTH + NMAP_GUID_SUFFIX_LENGTH - 1;
        for (i = 0; i < NMAP_GUID_SUFFIX_LENGTH; i++) {
            c = *ptr + 1;
            if (c < ':') {
                *ptr = c; /* '0' through '9' */
                break;
            } else if (c != ':') {
                if (c < 'G') {
                    *ptr = c; /* 'B' through 'F' */
                    break;
                }

                *ptr = '0';
                ptr--;
                continue;
            }

            *ptr = 'A';
            break;
        }

        if (i < NMAP_GUID_SUFFIX_LENGTH) {
            XplSignalLocalSemaphore(StoreAgent.guid.semaphore);

            return;
        }

        GuidReset();
        memcpy(guid, StoreAgent.guid.next, NMAP_GUID_LENGTH);

        XplSignalLocalSemaphore(StoreAgent.guid.semaphore);

        return;
    }

    return;
}
