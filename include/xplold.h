/*
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
 */
#ifndef XPLOLD_H
#define XPLOLD_H

/**********************
  Crypto Locks
 **********************/
void XPLCryptoLockInit(void);
void XPLCryptoLockDestroy(void);

#define NWDOS_NAME_SPACE     1
#define NWOS2_NAME_SPACE     6

#define XPLLongToDos(In, Out, OutSize)            strlen(strcpy(Out, In))
#define XPLDosToLong(In, PrefixLen, Out, OutSize) strlen(strcpy(Out, In + PrefixLen + 1))
#define NWSetNameSpaceEntryName(Path, Type, Newname)
#define NWGetNameSpaceEntryName(Path, Type, Len, Newname)       0
#define SetCurrentNameSpace(Type)
#define SetTargetNameSpace(Type)

#define d_nameDOS d_name

#endif
