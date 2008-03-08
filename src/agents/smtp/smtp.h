/****************************************************************************
 ** <Novell-copyright>
 ** Copyright (c) 2001 Novell, Inc. All Rights Reserved.
 **
 ** This program is free software; you can redistribute it and/or
 ** modify it under the terms of version 2 of the GNU General Public License
 ** as published by the Free Software Foundation.
 **
 ** This program is distributed in the hope that it will be useful,
 ** but WITHOUT ANY WARRANTY; without even the implied warranty of
 ** MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 ** GNU General Public License for more details.
 **
 ** You should have received a copy of the GNU General Public License
 ** along with this program; if not, contact Novell, Inc.
 **
 ** To contact Novell about this file by physical or electronic mail, you
 ** may find current contact information at www.novell.com.
 ** </Novell-copyright>
 *****************************************************************************/
// Parts Copyright (C) 2007-2008 Patrick Felt. See COPYING for details.

#ifndef SMTP_H
#define SMTP_H

/* SMTP Extensions */
#define EXT_DSN         (1<<0)
#define EXT_PIPELINING  (1<<1)
#define EXT_8BITMIME    (1<<2)
#define EXT_AUTH_LOGIN  (1<<3)
#define EXT_CHUNKING    (1<<4)
#define EXT_BINARYMIME  (1<<5)
#define EXT_SIZE        (1<<6)
#define EXT_ETRN        (1<<7)
#define EXT_TLS         (1<<8)

#endif
