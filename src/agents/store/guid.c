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

#include "stored.h"

#if HAVE_KSTAT_H
#include <kstat.h>
#elif HAVE_SYS_SYSINFO_H
#include <sys/sysinfo.h>
#endif

void GuidReset(void) {
	char name[256 + 1];
	char salt[32];
#ifdef HAVE_KSTAT_H
	kstat_t *ksp;
	kstat_ctl_t *kc;
#elifdef HAVE_SYS_SYSINFO_H
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

#if HAVE_KSTAT_H
	if ((kc = kstat_open()) &&
		(ksp = kstat_lookup(kc, "unix", 0, "system_misc")) &&
		(kstat_read(kc, ksp, NULL) != -1)) {
		XplHashWrite(&context, ksp, sizeof(kstat_t));
	} else {
		XplRandomData(name, sizeof(name) - 1);
		XplHashWrite(&context, name, sizeof(name) - 1);
	}
	if (kc != NULL) {
		kstat_close(kc);
	}
#elif HAVE_SYS_SYSINFO_H
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
