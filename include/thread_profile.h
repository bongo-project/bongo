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

#define ThreadCountInit(COUNTER)  XplMutexInit((COUNTER).mutex);
#define ThreadCountDestroy(COUNTER)  XplMutexDestroy((COUNTER).mutex);


typedef struct {
    XplMutex mutex;
    long value;
} ThreadCounter;


__inline static void
ThreadCountWrite(ThreadCounter *counter, long value)
{
    XplMutexLock(counter->mutex);
    counter->value = value;
    XplMutexUnlock(counter->mutex);
}


__inline static long
ThreadCountRead(ThreadCounter *counter)
{
    long locallong;

    XplMutexLock(counter->mutex);
    locallong = counter->value;
    XplMutexUnlock(counter->mutex);
    return(locallong);
}

__inline static void
ThreadCountIncrement(ThreadCounter *counter)
{
    XplMutexLock(counter->mutex);
    counter->value++;
    XplMutexUnlock(counter->mutex);
}

__inline static void
ThreadCountDecrement(ThreadCounter *counter)
{
    XplMutexLock(counter->mutex);
    counter->value--;
    XplMutexUnlock(counter->mutex);
}
