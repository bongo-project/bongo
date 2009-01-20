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

#ifndef CONNIOP_H
#define CONNIOP_H

#include <connio.h>

typedef struct _ConnIO {
    unsigned long timeOut;

    struct {
        XplSemaphore sem;

        Connection *head;
    } allocated;

    struct {
        BOOL enabled;
    } encryption;

    struct {
        BOOL enabled;
        unsigned long flags;
        XplSemaphore nextIdSem;
        unsigned long nextId;
        char path[XPL_MAX_PATH];
    } trace;
} ConnIOGlobals;

extern ConnIOGlobals ConnIO;

BOOL __gnutls_new(SSL_Context_and_Credentials *new_session, bongo_ssl_context *context, gnutls_connection_end_t con_end);

#endif
