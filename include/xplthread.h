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
 |***************************************************************************/

#ifndef XPLTHREAD_H
#define XPLTHREAD_H

#ifdef HAVE_SEMAPHORE_H
#include <semaphore.h>
#endif

typedef struct { volatile int counter; } XplAtomic;

/* this can be optimized so that it uses inc for 1, etc, but there is no real
 * reason to do that
 *
 * also, you could make a non-smp build that did not do lock
 */

#if defined(__i386__) || defined(__x86_64__)
static __inline__ void _XplSafeAdd (int i, XplAtomic *v)
{
	__asm__ __volatile__ (
		"lock; addl %1,%0"
		:"=m" (v->counter)
		:"ir" (i), "m" (v->counter));
}
#else /* we don't have any asm, so use a generic mutex-based definition */
#include <pthread.h>
static pthread_mutex_t __xpl_safe_add_mutex = PTHREAD_MUTEX_INITIALIZER;
static __inline__ void _XplSafeAdd (int i, XplAtomic *v)
{
    pthread_mutex_lock(&__xpl_safe_add_mutex);
    v->counter += i;
    pthread_mutex_unlock(&__xpl_safe_add_mutex);
}
#endif /* end of per-arch definition of _XplSafeAdd(); */

#define	XplSafeRead(Variable)         ((Variable).counter)
#define	XplSafeWrite(Variable, Value) (Variable).counter = (Value)
#define	XplSafeIncrement(Variable)    XplSafeAdd (Variable, 1)
#define XplSafeAdd(Variable, Value)   _XplSafeAdd((Value), &(Variable))
#define	XplSafeDecrement(Variable)    XplSafeAdd (Variable, -1)
#define	XplSafeSub(Variable, Value)   XplSafeAdd (Variable, -(Value))

#define XplThreadSwitchWithDelay()

/* Thread Groups */

#define XplGetThreadGroupID() getpid()
#define XplSetThreadGroupID(thread) thread=1
#define XplGetThreadID() pthread_self()
#define XplSetThreadID(thread)  

/* Threads */

#define XplThreadID pthread_t

#define XplBeginThread(pID, func, stack, args, ret)	{	pthread_attr_t thread_attr; 																					\
		pthread_attr_init(&thread_attr); 																			\
		pthread_attr_setdetachstate(&thread_attr, PTHREAD_CREATE_DETACHED);								\
		pthread_attr_setstacksize(&thread_attr, ((stack) * 2)); 												\
		if (((ret) = pthread_create((pID), &thread_attr, (void *)(func), (void *)(args))) == 0) {	\
			;																													\
		} else {																												\
			*(pID) = 0;																										\
		}																														\
		pthread_attr_destroy(&thread_attr);																			\
	}


#define XplBeginCountedThreadGroup							XplBeginCountedThread
#define XplBeginCountedThread(pID, func, stack, args, ret, counter)	{																										\
		pthread_attr_t thread_attr; 																					\
		pthread_attr_init(&thread_attr); 																			\
		pthread_attr_setdetachstate(&thread_attr, PTHREAD_CREATE_DETACHED);								\
		pthread_attr_setstacksize(&thread_attr, ((stack) * 2)); 												\
		XplSafeIncrement (counter);																			\
		if (((ret) = pthread_create(pID, &thread_attr, (void *)(func), (void *)(args))) == 0) {	\
			;																													\
		} else {																												\
			*(pID) = 0;																										\
			XplSafeDecrement (counter);																			\
		}																														\
		pthread_attr_destroy(&thread_attr);																			\
	}

#define XplExitThread(code, status)							pthread_exit((void *)status)
#define XplSuspendThread(id)									;
#define XplResumeThread(id)									;
#define XplRenameThread(id, Name)							;

#define XplMutex                                pthread_mutex_t    
#define XplMutexInit(m)                         pthread_mutex_init(&(m), 0);
#define XplMutexLock(m)                         pthread_mutex_lock(&(m));
#define XplMutexTryLock(m)                      pthread_mutex_trylock(&(m));
#define XplMutexUnlock(m)                       pthread_mutex_unlock(&(m));
#define XplMutexDestroy(m)                      pthread_mutex_destroy(&(m));

#define XplSemaphore									sem_t
#define XplOpenLocalSemaphore(sem, init)						sem_init(&(sem), 0, init)
#define XplCloseLocalSemaphore(sem)							sem_destroy(&(sem))
#define XplWaitOnLocalSemaphore(sem)							sem_wait(&(sem))
#define XplSignalLocalSemaphore(sem)							sem_post(&(sem))
#define XplExamineLocalSemaphore(sem, cnt)						sem_getvalue(&(sem), (int *)&(cnt))

#endif
