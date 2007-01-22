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

#ifndef _BONGO_RESOLVE_H
#define _BONGO_RESOLVE_H

#include <config.h>
#include <xpl.h>

/* XplDNSResolve() returns a linked list of these structures */
#ifdef __cplusplus
extern "C" {
#endif

#if defined(WIN32) || defined(NETWARE) || defined(LIBC)
#pragma pack (push, 1)
#endif

typedef struct _XplMxRecord {
    char name[MAXEMAILNAMESIZE + 1];    /* The name of the mail exchanger */
    struct in_addr addr;
    unsigned short preference;
} Xpl8BitPackedStructure XplMxRecord;

typedef struct _XplARecord {
    char name[MAXEMAILNAMESIZE + 1];    /* The name of the mail exchanger */
    struct in_addr addr;
} Xpl8BitPackedStructure XplARecord;

typedef struct _XplPtrRecord {
    char name[MAXEMAILNAMESIZE + 1];    /* The name of the mail exchanger */
}  Xpl8BitPackedStructure XplPtrRecord;

typedef struct _XplSoaRecord {
    char mname[MAXEMAILNAMESIZE + 1];   /* The name of the mail exchanger */
    char rname[MAXEMAILNAMESIZE + 1];   /* The name of the mail exchanger */
    unsigned long serial;               /* The version number of the zone */
    unsigned long refresh;              /* zone refresh interval (sec)    */
    unsigned long retry;                /* time to wait after failed refrsh*/
    unsigned long expire;               /* max Time to keep data (sec)    */
    unsigned long minimum;              /* TTL for zone (sec)             */
} Xpl8BitPackedStructure XplSoaRecord;
    

typedef union _XplDnsRecord {
    XplARecord A;
    XplMxRecord MX;
    XplPtrRecord PTR;
    XplSoaRecord SOA;
} Xpl8BitPackedStructure XplDnsRecord;

#if defined(WIN32) || defined(NETWARE) || defined(LIBC)
#pragma pack (pop)
#endif

/* RR type constants */
#define XPL_RR_A        0x0001    /* Host address */
#define XPL_RR_CNAME    0x0005    /* Canonical name (alias) */
#define XPL_RR_SOA      0x0006    /* Start of Zone Authority */
#define XPL_RR_PTR      0x000C    /* Domain Name Pointer */
#define XPL_RR_MX       0x000F    /* Mail exchanger */
#define XPL_RR_MX       0x000F    /* Mail exchanger */

/* Return codes for MX */
#define XPL_DNS_SUCCESS     0
#define XPL_DNS_BADHOSTNAME -1
#define XPL_DNS_FAIL        -2
#define XPL_DNS_TIMEOUT     -3
#define XPL_DNS_NORECORDS   -4

/* Prototypes */
EXPORT int XplDnsResolve(char *host, XplDnsRecord **list, int type);

EXPORT void XplDnsAddResolver(const char *resolverValue);

EXPORT BOOL XplResolveStart (void);
EXPORT BOOL XplResolveStop (void);    

#ifdef __cplusplus
}
#endif

#endif /* _BONGO_RESOLVE_H */
