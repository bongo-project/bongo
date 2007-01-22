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

#ifndef MSGAPIP_H
#define MSGAPIP_H

#include <xpl.h>
#include <mdb.h>

#include <msgapi.h>

/*  msgapi.c */
MDBHandle MsgDirectoryHandle(void);
BOOL MsgExiting(void);

/*  msgdate.c */
void MsgDateSetUTCOffset(long offset);
BOOL MsgDateStart(void);

/*  resolver.c */
BOOL MsgResolveStart(void);
BOOL MsgResolveStop(void);

#endif /* MSGAPIP_H */
