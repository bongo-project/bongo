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

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <errno.h>

#include <pthread.h>

void XPLCryptoLockInit(void);
void XPLCryptoLockDestroy(void);

static void CryptoLockCallback(int mode,int type,char *file,int line);
#ifdef SOLARIS
static unsigned long solaris_thread_id(void );
#endif
static unsigned long pthreads_thread_id(void );

/* usage:
 * XPLCryptoLockInit();
 * application code
 * XPLCryptoLockDestroy();
 */

#ifdef WIN32

static HANDLE *CryptoLocks = NULL;

void 
XPLCryptoLockInit(void)
{
    int i;

    if (!CryptoLocks) {
        CryptoLocks = malloc(CRYPTO_num_locks() * sizeof(HANDLE));

        for (i = 0; i < CRYPTO_num_locks(); i++)
        {
            CryptoLocks[i] = CreateMutex(NULL,FALSE,NULL);
        }

        CRYPTO_set_locking_callback((void (*)(int,int,const char *,int))CryptoLockCallback);
    }

    return;
}

void 
XPLCryptoLockDestroy(void)
{
    int i;

    if (CryptoLocks) {
        CRYPTO_set_locking_callback(NULL);

        for (i = 0; i < CRYPTO_num_locks(); i++) {
            CloseHandle(CryptoLocks[i]);
        }

        free(CryptoLocks);
        CryptoLocks = NULL;
    }
}

void 
CryptoLockCallback(int mode, int type, char *file, int line)
{
    if (mode & CRYPTO_LOCK)    {
        WaitForSingleObject(CryptoLocks[type],INFINITE);
    } else {
        ReleaseMutex(CryptoLocks[type]);
    }
}

#endif /* WIN32 */

#ifdef SOLARIS

#define USE_MUTEX

#ifdef USE_MUTEX
static mutex_t *CryptoLocks = NULL;
#else
static rwlock_t *CryptoLocks = NULL;
#endif
static long *CryptoLockCount = NULL;

void 
XPLCryptoLockInit(void)
{
    int i;

    if (!CryptoLocks && !CryptoLockCount) {
#ifdef USE_MUTEX
        CryptoLocks = malloc(CRYPTO_num_locks() * sizeof(mutex_t));
#else
        CryptoLocks = malloc(CRYPTO_num_locks() * sizeof(rwlock_t));
#endif
        CryptoLockCount = malloc(CRYPTO_num_locks() * sizeof(long));

        if (CryptoLocks && CryptoLockCount) {
            for (i = 0; i < CRYPTO_num_locks(); i++) {
                CryptoLockCount[i] = 0;

#ifdef USE_MUTEX
                mutex_init(&(CryptoLocks[i]),USYNC_THREAD,NULL);
#else
                rwlock_init(&(CryptoLocks[i]),USYNC_THREAD,NULL);
#endif
            }

            CRYPTO_set_id_callback((unsigned long (*)())solaris_thread_id);
            CRYPTO_set_locking_callback((void (*)())CryptoLockCallback);

            return;
        }

        if (CryptoLocks) {
            free(CryptoLocks);
            CryptoLocks = NULL;
        }

        if (CryptoLockCount) {
            free(CryptoLockCount);
            CryptoLockCount = NULL;
        }
    }

    return;
}

void 
XPLCryptoLockDestroy(void)
{
    int i;

    if (CryptoLocks) {
        CRYPTO_set_locking_callback(NULL);

        for (i = 0; i < CRYPTO_num_locks(); i++) {
#ifdef USE_MUTEX
            mutex_destroy(&(CryptoLocks[i]));
#else
            rwlock_destroy(&(CryptoLocks[i]));
#endif
        }

        free(CryptoLocks);
        CryptoLocks = NULL;
    }

    if (CryptoLockCount) {
        free(CryptoLockCount);
        CryptoLockCount = NULL;
    }

}

void 
CryptoLockCallback(int mode, int type, char *file, int line)
{
#if 0
    fprintf(stderr,"thread=%4d mode=%s lock=%s %s:%d\n",
        CRYPTO_thread_id(),
        (mode&CRYPTO_LOCK)?"l":"u",
        (type&CRYPTO_READ)?"r":"w",file,line);
#endif

#if 0
    if (CRYPTO_LOCK_SSL_CERT == type)
        fprintf(stderr,"(t,m,f,l) %ld %d %s %d\n",
            CRYPTO_thread_id(),
            mode,file,line);
#endif

    if (mode & CRYPTO_LOCK) {
#ifdef USE_MUTEX
        mutex_lock(&(CryptoLocks[type]));
#else
        if (mode & CRYPTO_READ) {
            rw_rdlock(&(CryptoLocks[type]));
        } else {
            rw_wrlock(&(CryptoLocks[type]));
        }
#endif

        CryptoLockCount[type]++;
    } else {
#ifdef USE_MUTEX
        mutex_unlock(&(CryptoLocks[type]));
#else
        rw_unlock(&(CryptoLocks[type]));
#endif
    }

    return;
}

unsigned long 
solaris_thread_id(void)
{
    unsigned long ret;

    ret = (unsigned long)thr_self();
    return(ret);
}
#endif /* SOLARIS */


/* Linux and a few others */
#if defined(LINUX) || defined(S390RH)

static pthread_mutex_t *CryptoLocks = NULL;
static long *CryptoLockCount = NULL;

void 
XPLCryptoLockInit(void)
{
/*
    int i;

    if (!CryptoLocks && !CryptoLockCount) {
        CryptoLocks = malloc(CRYPTO_num_locks() * sizeof(pthread_mutex_t));
        CryptoLockCount = malloc(CRYPTO_num_locks() * sizeof(long));

        if (CryptoLocks && CryptoLockCount) {
            for (i = 0; i < CRYPTO_num_locks(); i++) {
                CryptoLockCount[i] = 0;
                pthread_mutex_init(&(CryptoLocks[i]), NULL);
            }

            CRYPTO_set_id_callback((unsigned long (*)())pthreads_thread_id);
            CRYPTO_set_locking_callback((void (*)())CryptoLockCallback);

            return;
        }

        if (CryptoLocks) {
            free(CryptoLocks);
            CryptoLocks = NULL;
        }

        if (CryptoLockCount) {
            free(CryptoLockCount);
            CryptoLockCount = NULL;
        }
    } */

    return;
}

void XPLCryptoLockDestroy(void)
{
/*
    int i;

    if (CryptoLocks) {
        CRYPTO_set_locking_callback(NULL);
        for (i = 0; i < CRYPTO_num_locks(); i++) {
            pthread_mutex_destroy(&(CryptoLocks[i]));
        }

        free(CryptoLocks);
        CryptoLocks = NULL;
    }

    if (CryptoLockCount) {
        free(CryptoLockCount);
        CryptoLockCount = NULL;
    }
*/
    return;
}

void 
CryptoLockCallback(int mode, int type, char *file, int line)
{
/*
#if 0
    fprintf(stderr,"thread=%4d mode=%s lock=%s %s:%d\n",CRYPTO_thread_id(), (mode&CRYPTO_LOCK)?"l":"u", (type&CRYPTO_READ)?"r":"w",file,line);
#endif
#if 0
    if (CRYPTO_LOCK_SSL_CERT == type) {
        fprintf(stderr,"(t,m,f,l) %ld %d %s %d\n", CRYPTO_thread_id(), mode,file,line);
    }
#endif
    if (mode & CRYPTO_LOCK) {
        pthread_mutex_lock(&(CryptoLocks[type]));
        CryptoLockCount[type]++;
    } else {
        pthread_mutex_unlock(&(CryptoLocks[type]));
    }
    */
}

unsigned long 
pthreads_thread_id(void)
{
    unsigned long ret;

    ret = (unsigned long)pthread_self();
    return(ret);
}
#endif /* PTHREADS */

#if defined(LIBC)
//static pthread_mutex_t *CryptoLocks = NULL;
//static long *CryptoLockCount = NULL;

unsigned long 
CryptoIDCallback(void)
{
    return((unsigned long)pthread_self());
}

void 
CryptoLockCallback(int mode, int type, char *file, int line)
{
/*
#if 0
    consoleprintf("thread=%4d mode=%s lock=%s %s:%d\n", CRYPTO_thread_id(), (mode & CRYPTO_LOCK)? "l": "u", (type & CRYPTO_READ)? "r": "w", file, line);

    if (CRYPTO_LOCK_SSL_CERT == type) {
        consoleprintf("(t,m,f,l) %ld %d %s %d\n", CRYPTO_thread_id(), mode, file, line);
    }
#endif

    if (mode & CRYPTO_LOCK) {
        pthread_mutex_lock(&(CryptoLocks[type]));
        CryptoLockCount[type]++;
    } else {
        pthread_mutex_unlock(&(CryptoLocks[type]));
    }

    return;
    */
}

void 
XPLCryptoLockInit(void)
{
/*
    int i;

    if (!CryptoLocks && !CryptoLockCount) {
        CryptoLocks = malloc(CRYPTO_num_locks() * sizeof(pthread_mutex_t));
        CryptoLockCount = malloc(CRYPTO_num_locks() * sizeof(long));

        if (CryptoLocks && CryptoLockCount) {
            for (i = 0; i < CRYPTO_num_locks(); i++) {
                CryptoLockCount[i] = 0;
                pthread_mutex_init(&(CryptoLocks[i]), NULL);
            }

            CRYPTO_set_id_callback((unsigned long (*)())CryptoIDCallback);
            CRYPTO_set_locking_callback((void (*)())CryptoLockCallback);

            return;
        }

        if (CryptoLocks) {
            free(CryptoLocks);
            CryptoLocks = NULL;
        }

        if (CryptoLockCount) {
            free(CryptoLockCount);
            CryptoLockCount = NULL;
        }
    }

    return;
    */
}

void 
XPLCryptoLockDestroy(void)
{
/*
    int i;

    if (CryptoLocks) {
        CRYPTO_set_locking_callback(NULL);

        for (i = 0; i < CRYPTO_num_locks(); i++) {
            pthread_mutex_destroy(&(CryptoLocks[i]));
        }

        free(CryptoLocks);
        CryptoLocks = NULL;
    }

    if (CryptoLockCount) {
        free(CryptoLockCount);
        CryptoLockCount = NULL;
    }

    return;
    */
}

#endif

#if defined(NETWARE)
void 
XPLCryptoLockInit(void)
{
    return;
}

void 
XPLCryptoLockDestroy(void)
{
    return;
}
#endif
