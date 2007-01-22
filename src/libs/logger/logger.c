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
#include "loggerp.h"
/* Prototypes */

EXPORT void
LoggerEvent(LoggingHandle *handle, const char *subsystem, unsigned long eventId, int level, int flags, const char *str1, const char *str2, int i1, int i2, void *p, int size)
{
    char buf[1024];
    unsigned int n;
    
    n = LoggerFormatMessage(eventId, buf, sizeof(buf), str1, str2, i1, i2, p, size);
    
    if (n < sizeof(buf) && n > 0) {
        LogMsg(subsystem, eventId, level, "%s", buf);
    }
}

EXPORT void
LoggerClose(LoggingHandle *handle)
{
    log4c_fini();
}

EXPORT LoggingHandle *
LoggerOpen(const char *name)
{
    log4c_init();
    
    return (LoggingHandle*)1;
}
