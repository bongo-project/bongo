/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2005 Novell, Inc. All Rights Reserved.
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



#ifdef HAVE_NANOSLEEP

void
XplDelayNanosleep(int msec)
{
    struct timespec timeout;
    timeout.tv_sec = (msec) / 1000;
    timeout.tv_nsec = ((msec) % 1000) * 1000000;
    nanosleep(&timeout, NULL);
}

#endif

void
XplDelaySelect(int msec)
{
    struct timeval timeout;
    timeout.tv_sec = (msec) / 1000;
    timeout.tv_usec = ((msec) % 1000) * 1000;
    select(0, NULL, NULL, NULL, &timeout);
}
