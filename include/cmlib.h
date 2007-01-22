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

#ifndef _BONGO_CM_LIBRARY_H
#define _BONGO_CM_LIBRARY_H

#include <mdb.h>

#include <connio.h>
#include <connmgr.h>

/*
    CMInitialize() MUST be called before any other call in the library.
    If it is not called the other calls will be unable to authenticate
    to the connection mananager.
*/
BOOL CMInitialize(MDBHandle directoryHandle, unsigned char *service);

/*
    Request permission for the specified address to connect to the specified
    service.

    CM_RESULT_ALLOWED will be returned if the connection is allowed.  If not
    CM_RESULT_DENY_TEMPORARY or CM_RESULT_DENY_PERMANENT will be returned.
*/
int CMVerifyConnect(unsigned long address, unsigned char *comment, BOOL *requireAuth);

/*
    Request permission for the specified address to relay.

    CM_RESULT_ALLOWED will be returned if the connection is allowed.  If not
    CM_RESULT_DENY_TEMPORARY or CM_RESULT_DENY_PERMANENT will be returned.

    if CM_RESULT_ALLOWED is returned the buffer pointed to by user will be
    filled out with the name of the user that authenticated.
*/
int CMVerifyRelay(unsigned long address, unsigned char *user);

/*
    Inform the Connection Manager that a connection started with CMConnect()
    has ended.

    The result will be CM_RESULT_OK if the notification was sent succesfully.
*/
int CMDisconnected(unsigned long address);

/*
    Inform the Connection Manager that a user has authenticated from the
    specified address.

    The result will be CM_RESULT_OK if the notification was sent succesfully.
*/
int CMAuthenticated(unsigned long address, unsigned char *user);

int CMDelivered(unsigned long address, unsigned long recipients);
int CMReceived(unsigned long address, unsigned long local, unsigned long invalid, unsigned long remote);
int CMFoundVirus(unsigned long address, unsigned long recipients, unsigned char *name);
int CMSpamFound(unsigned long address, unsigned long recipients);
int CMDOSDetected(unsigned long address, unsigned char *description);

/*
    Send a custom command object to the Connection Manager.  You do not
    need to fill out the pass field.  The library will take care of
    authentication for you.  If the request was sent succesfully TRUE
    will be returned and the result object will be filled out.
*/
BOOL CMSendCommand(ConnMgrCommand *command, ConnMgrResult *result);

#endif  /* _BONGO_CM_LIBRARY_H */
