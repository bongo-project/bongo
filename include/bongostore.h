/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2006 Novell, Inc. All Rights Reserved.
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

#define BONGO_STORE_DEFAULT_PORT 689

typedef enum {

    STORE_DOCTYPE_UNKNOWN          = 0x0001,
    STORE_DOCTYPE_MAIL             = 0x0002,
    STORE_DOCTYPE_EVENT            = 0x0003, /* event, jcal */
    STORE_DOCTYPE_AB               = 0x0004, /* addressbook entry, jcard */
    STORE_DOCTYPE_CONVERSATION     = 0x0005,
    STORE_DOCTYPE_CALENDAR         = 0x0006,
    STORE_DOCTYPE_CONFIG           = 0x0007, /* agent configuration, json */

    STORE_DOCTYPE_FOLDER           = 0x1000,

    STORE_DOCTYPE_SHARED           = 0x2000,
/*
    STORE_DOCTYPE_UNKNOWN_IN_SHARE = STORE_DOCTYPE_SHARED | STORE_DOCTYPE_UNKNOWN,
    STORE_DOCTYPE_MAIL_IN_SHARE    = STORE_DOCTYPE_SHARED | STORE_DOCTYPE_MAIL,
    STORE_DOCTYPE_CAL_IN_SHARE     = STORE_DOCTYPE_SHARED | STORE_DOCTYPE_CAL,
    STORE_DOCTYPE_AB_IN_SHARE      = STORE_DOCTYPE_SHARED | STORE_DOCTYPE_AB,
    STORE_DOCTYPE_CONVERSATION_IN_SHARE = STORE_DOCTYPE_SHARED | STORE_DOCTYPE_CONVERSATION,

    STORE_DOCTYPE_SHARED_FOLDER    = STORE_DOCTYPE_SHARED | STORE_DOCTYPE_FOLDER,

    STORE_DOCTYPE_PURGED = 0x4000,
*/
} StoreDocumentType;


/** Global document flags (bits 16-31) **/

#define STORE_DOCUMENT_FLAG_STARS (3 << 16)


/** Type-specific document flags (bits 0-15) **/

/* DOCTYPE_MAIL flags: */
#define STORE_MSG_FLAG_PURGED   (1 << 0)
#define STORE_MSG_FLAG_SEEN     (1 << 1)
#define STORE_MSG_FLAG_ANSWERED (1 << 2)
#define STORE_MSG_FLAG_FLAGGED  (1 << 3)
#define STORE_MSG_FLAG_DELETED  (1 << 4)
#define STORE_MSG_FLAG_DRAFT    (1 << 5)
#define STORE_MSG_FLAG_RECENT   (1 << 6)
#define STORE_MSG_FLAG_JUNK     (1 << 7)
#define STORE_MSG_FLAG_SENT     (1 << 8)

/* DOCTYPE_COLLECTION flags: */
#define STORE_COLLECTION_FLAG_HIERARCHY_ONLY (1 << 0)
#define STORE_COLLECTION_FLAG_NON_SUBSCRIBED (1 << 1)


/** end of document flags **/


#define STORE_MAX_COLLECTION    256  /* longest collection name, including '\0' */
#define STORE_MAX_PATH          384  /* longest path name, including '\0' */
#define STORE_MAX_STORENAME     256  /* longest user or store name, including '\0' */

/* Access control flags */

typedef enum {
    STORE_PRIV_WRITE =               1 << 1,  /* DAV:write - NOT USED IN BONGO */
    STORE_PRIV_WRITE_PROPS =         1 << 2,  /* DAV:write-properties */
    STORE_PRIV_WRITE_CONTENT =       1 << 3,  /* DAV:write-content */
    STORE_PRIV_UNLOCK =              1 << 4,  /* DAV:unlock */
    STORE_PRIV_READ_ACL =            1 << 5,  /* DAV:read-acl - NOT IMPLEMENTED */
    STORE_PRIV_READ_OWN_ACL =        1 << 6,  /* DAV:read-current-user-privilege-set 
                                                - NOT IMPLEMENTED */
    STORE_PRIV_WRITE_ACL =           1 << 7,  /* DAV:write-acl */
    STORE_PRIV_BIND =                1 << 8,  /* DAV:bind */
    STORE_PRIV_UNBIND =              1 << 9,  /* DAV:unbind */
    STORE_PRIV_READ_BUSY =           1 << 10, /* access to free-busy info */

    STORE_PRIV_READ_PROPS =          1 << 11,
    STORE_PRIV_LIST =                1 << 12,

    STORE_PRIV_READ =                1 << 0 | /* DAV:read */
                                     STORE_PRIV_READ_PROPS |
                                     STORE_PRIV_READ_ACL | 
                                     STORE_PRIV_READ_OWN_ACL | 
                                     STORE_PRIV_READ_BUSY| 
                                     STORE_PRIV_LIST,

    STORE_PRIV_ALL =                 0xffffffff,
} StorePrivilege;

typedef enum {
    STORE_PRINCIPAL_NONE = 0,
    STORE_PRINCIPAL_ALL = 1,           // anyone at all
    STORE_PRINCIPAL_AUTHENTICATED = 2, // any authenticated user
    STORE_PRINCIPAL_USER = 3,          // a particular user
    STORE_PRINCIPAL_GROUP = 4,         // a member of a particular group
    STORE_PRINCIPAL_TOKEN = 5         // a particular non-user, id'ed by their token
} StorePrincipalType;


#ifdef HAVE_INTTYPES_H
#define __STDC_FORMAT_MACROS
#include <inttypes.h>
#define FMT_UINT64_HEX "%" PRIxFAST64
#define FMT_UINT64_DEC "%" PRIuFAST64
#define STORE_GUID_FMT "%016" PRIxFAST64
#else
#error You don not have inttypes.h available. This will cause compile problems.
#define FMT_UINT64_HEX "%llx"
#define FMT_UINT64_DEC "%llu"
#define STORE_GUID_FMT "%016llx"
#endif
