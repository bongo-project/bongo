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

#include "bongo-config.h"

#ifdef _BONGO_HAVE_SEMAPHORE_H

#include <semaphore.h>

#endif

/* Atomic reads/writes */

#if defined(_BONGO_LINUX)
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
#elif defined (__powerpc__) || defined(__POWERPC__)
/* this can be optimized so that it uses inc for 1, etc, but there is no real
 * reason to do that
 *
 * also, you could make a non-smp build that did not do lock
 */
static __inline__ void _XplSafeAdd (int a, XplAtomic *v)
{
    int t;
	__asm__ __volatile__(
"1:     lwarx   %0,0,%3\n\
        add     %0,%2,%0\n\
        stwcx.  %0,0,%3 \n\
        bne-    1b"
        : "=&r" (t), "=m" (v->counter)
        : "r" (a), "r" (&v->counter), "m" (v->counter)
: "cc");
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

#elif defined(WIN32)

#define	XplAtomic												unsigned long
#define	XplSafeRead(Variable)								(Variable)
#define	XplSafeWrite(Variable, Value)						InterlockedExchange(&(Variable), (Value))
#define	XplSafeIncrement(Variable)							InterlockedIncrement(&(Variable))
#define	XplSafeDecrement(Variable)							InterlockedDecrement(&(Variable))
#define	XplSafeAdd(Variable, Value)						InterlockedExchangeAdd(&(Variable), (Value))
#define	XplSafeSub(Variable, Value)						InterlockedExchangeAdd(&(Variable), -1*(Value))

#else
# error "Safe variable operations not implemented on this platform"
#endif



/* FIXME: should be #ifdef PTHREAD */
#ifdef _BONGO_LINUX

/* FIXME: This is only implemented for pthreads, need to bring in 
 * the netware/windows code at some point */

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

#elif defined(WIN32)

#include <process.h>

#define XplThreadSwitchWithDelay()

/* Thread Groups */

#define XplGetThreadGroupID()	                   (HANDLE)GetModuleHandle(NULL)
#define XplSetThreadGroupID(thread)				   (HANDLE)GetModuleHandle(NULL)
#define XplGetThreadID()		                   (HANDLE)GetModuleHandle(NULL)
#define XplSetThreadID(thread)                     (int)GetModuleHandle(NULL)

/* Threads */

#define XplThreadID HANDLE

#define XplBeginThread(id, func, stack, args, ret)	{	unsigned long tHrHndl;																																										\
																			if ((tHrHndl = _beginthreadex(NULL, (stack), (unsigned int (__stdcall *)(void *))(func), (void *)(args), 0, (unsigned int *)(id))) != 0) {	\
																				CloseHandle((HANDLE)tHrHndl);																																							\
																				(ret) = 0;																																											\
																			} else {																																													\
																				(ret) = -1;																																											\
																			}																																															\
																		}

#define XplBeginCountedThreadGroup							XplBeginCountedThread
#define XplBeginCountedThread(id, func, stack, args, ret, counter)	{																																											\
																			unsigned long tHrHndl;																																										\
																			InterlockedIncrement(&(counter));																																				\
																			if ((tHrHndl = _beginthreadex(NULL, (stack), (unsigned int (__stdcall *)(void *))(func), (void *)(args), 0, (unsigned int *)(id))) != 0) {	\
																				CloseHandle((HANDLE)tHrHndl);																																							\
																				(ret) = 0;																																											\
																			} else {																																													\
																				InterlockedDecrement(&(counter));																																			\
																				(ret) = -1;																																											\
																			}																																															\
                                                                        }
#define XplExitThread(code, status)							_endthreadex(status)
#define XplSuspendThread(id)								SuspendThread(id)
#define XplResumeThread(id)									ResumeThread(id)
#define XplRenameThread(id, Name)							

#define XplMutex                                HANDLE    
#define XplMutexInit(m)                         (m) = CreateMutex(NULL, FALSE, NULL)
#define XplMutexLock(m)                         (WaitForSingleObject((m), INFINITE) == WAIT_OBJECT_0)
#define XplMutexTryLock(m)                      (WaitForSingleObject((m), 0) != WAIT_TIMEOUT)
#define XplMutexUnlock(m)                       (ReleaseMutex(m))
#define XplMutexDestroy(m)                      (CloseHandle(m))

#endif


#ifdef _BONGO_HAVE_SEMAPHORE_H

/* Semaphores */
 /**********************
  Semaphores
 **********************/

#ifdef MACOSX

#include <Multiprocessing.h>
#include <MacErrors.h>

#define XplSemaphore									MPSemaphoreID
#define XplOpenLocalSemaphore(sem, init)						(MPCreateSemaphore(0x7fffffff,init,&sem) != noErr ? -1 : (int) sem)
#define XplCloseLocalSemaphore(sem)							(MPDeleteSemaphore(sem) != noErr ? -1 : 0)
#define XplWaitOnLocalSemaphore(sem)							(MPWaitOnSemaphore(sem,kDurationForever) != noErr ? -1 : 0)
#define XplSignalLocalSemaphore(sem)							(MPSignalSemaphore(sem) != noErr ? -1 : 0)
#define XplExamineLocalSemaphore(sem, cnt)						if (MPWaitOnSemaphore(sem,kDurationImmediate) != kMPTimeoutErr) { MPSignalSemaphore(sem); cnt = 1; } else { cnt = 0; }

#else

#define XplSemaphore									sem_t
#define XplOpenLocalSemaphore(sem, init)						sem_init(&sem, 0, init)
#define XplCloseLocalSemaphore(sem)							sem_destroy(&sem)
#define XplWaitOnLocalSemaphore(sem)							sem_wait(&sem)
#define XplSignalLocalSemaphore(sem)							sem_post(&sem)
#define XplExamineLocalSemaphore(sem, cnt)						sem_getvalue(&sem, (int *)&cnt)

#endif

#elif defined(WIN32)

#define	XplSemaphore								HANDLE
#define	XplOpenLocalSemaphore(sem, init)		sem=CreateSemaphore(NULL, init, 0x7fffffff, NULL);
#define	XplCloseLocalSemaphore(sem)			CloseHandle(sem)
#define	XplWaitOnLocalSemaphore(sem)			(WaitForSingleObject(sem, INFINITE)==WAIT_FAILED)
#define	XplSignalLocalSemaphore(sem)			(ReleaseSemaphore(sem, 1, NULL)==0)
#define	XplExamineLocalSemaphore(sem, cnt)	if (WaitForSingleObject(sem, 0)!=WAIT_TIMEOUT) { ReleaseSemaphore(sem, 1, (LPLONG)&cnt); cnt++;} else { cnt=0; }

#endif

#endif
