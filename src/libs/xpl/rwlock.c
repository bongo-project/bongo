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
#include <xpl.h>

#define	RWLOCK_INITIALIZED		(1<<0)
#define	RWLOCK_READ					0
#define	RWLOCK_WRITE				1

BOOL
XplRWLockInit(XplRWLock *RWLock)
{
	if (RWLock == NULL) {
		return(FALSE);
	}
	RWLock->RWState = RWLOCK_INITIALIZED;
	RWLock->RWReaders = 0;
	XplOpenLocalSemaphore(RWLock->RWLock, 1);
	XplOpenLocalSemaphore(RWLock->RWMutexRead, 1);
	XplOpenLocalSemaphore(RWLock->RWMutexReadWrite, 1);

	return(TRUE);
}

BOOL
XplRWLockDestroy(XplRWLock *RWLock)
{
	if (RWLock == NULL) {
		return(FALSE);
	}

	XplRWWriteLockAcquire(RWLock);
	RWLock->RWState=0;		/* Void the structure */
	XplCloseLocalSemaphore(RWLock->RWLock);
	XplCloseLocalSemaphore(RWLock->RWMutexRead);
	XplCloseLocalSemaphore(RWLock->RWMutexReadWrite);
	return(TRUE);
}

BOOL
XplRWReadLockAcquire(XplRWLock *RWLock)
{
    /* consistency checks */
	if (RWLock == NULL) {
		return(FALSE);
	}

	if (!(RWLock->RWState & RWLOCK_INITIALIZED)) {
		return(FALSE);
	}

	if (XplWaitOnLocalSemaphore(RWLock->RWLock)!=0) {
		return(FALSE);
	}
	XplSignalLocalSemaphore(RWLock->RWLock);

	/* acquire lock */
	if (XplWaitOnLocalSemaphore(RWLock->RWMutexRead)!=0) {
		return(FALSE);
	}

	RWLock->RWReaders++;

	if (RWLock->RWReaders == 1) {
		if (XplWaitOnLocalSemaphore(RWLock->RWMutexReadWrite)!=0) {
			RWLock->RWReaders--;
			XplSignalLocalSemaphore(RWLock->RWMutexRead);
			return(FALSE);
		}
	}
	RWLock->RWMode = RWLOCK_READ;
	XplSignalLocalSemaphore(RWLock->RWMutexRead);
	return(TRUE);
}

BOOL
XplRWWriteLockAcquire(XplRWLock *RWLock)
{
    /* consistency checks */
	if (RWLock == NULL) {
		return(FALSE);
	}

	if (!(RWLock->RWState & RWLOCK_INITIALIZED)) {
		return(FALSE);
	}

	/* acquire lock */
	if (XplWaitOnLocalSemaphore(RWLock->RWLock)!=0) {
		return(FALSE);
	}

	if (XplWaitOnLocalSemaphore(RWLock->RWMutexReadWrite)!=0) {
		return(FALSE);
	}

	RWLock->RWMode = RWLOCK_WRITE;
	return(TRUE);
}

BOOL
XplRWReadLockRelease(XplRWLock *RWLock)
{
	/* consistency checks */
	if (RWLock == NULL) {
		return(FALSE);
	}

	if (!(RWLock->RWState & RWLOCK_INITIALIZED)) {
		return(FALSE);
	}

	/* release lock */
	if (XplWaitOnLocalSemaphore(RWLock->RWMutexRead)!=0) {
		return FALSE;
	}
	RWLock->RWReaders--;
	if (RWLock->RWReaders == 0) {
		if (XplSignalLocalSemaphore(RWLock->RWMutexReadWrite)!=0) {
			RWLock->RWReaders++;
			XplSignalLocalSemaphore(RWLock->RWMutexRead);
			return(FALSE);
		}
	}
	RWLock->RWMode = RWLOCK_READ;
	XplSignalLocalSemaphore(RWLock->RWMutexRead);
	return(TRUE);
}

BOOL
XplRWWriteLockRelease(XplRWLock *RWLock)
{
	/* consistency checks */
	if (RWLock == NULL) {
		return(FALSE);
	}

	if (!(RWLock->RWState & RWLOCK_INITIALIZED)) {
		return(FALSE);
	}

    /* release lock */
	if (XplSignalLocalSemaphore(RWLock->RWMutexReadWrite)!=0) {
		return FALSE;
	}
	XplSignalLocalSemaphore(RWLock->RWLock);
	return(TRUE);
}
