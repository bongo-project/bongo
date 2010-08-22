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

#ifndef XPLUTIL_H
#define XPLUTIL_H

#include <config.h>
#include "xplthread.h"

#include <ctype.h>
#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <signal.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>

#ifdef HAVE_SYS_STAT_H
#include <sys/stat.h>
#endif

# include <dirent.h>
# include <netdb.h>
# include <netinet/in.h>
# include <sys/param.h>
# include <sys/poll.h>
# include <sys/socket.h>
# include <sys/time.h>
# include <sys/types.h>
# include <arpa/inet.h>
# include <unistd.h>

/* Basic Definitions */

typedef int BOOL;

typedef	unsigned char BYTE;
typedef	unsigned short WORD;

#ifndef _UNICODE_TYPE_DEFINED
#define _UNICODE_TYPE_DEFINED
typedef unsigned short unicode;
#endif

#ifndef FALSE
# define FALSE (0)
#endif

#ifndef TRUE
# define TRUE (!FALSE)
#endif

#ifndef min
# define min(a,b)                                              (((a)<(b)) ? (a) : (b))
#endif

#ifndef max
# define max(a,b)                                               (((a)<(b)) ? (b) : (a))
#endif

/* Branch hints */
#if defined(__GNUC__) && (__GNUC__ > 2)
#define XPL_LIKELY(e) (__builtin_expect(!!(e), 1))
#define XPL_UNLIKELY(e) (__builtin_expect(!!(e), 0))
#else
#define XPL_LIKELY(e) (e)
#define XPL_UNLIKELY(e) (e)
#endif

#define EXPORT

/* Format string helpers */
#if defined(__GNUC__) && (__GNUC__ > 2)
#define XPL_PRINTF(format_idx, arg_idx) __attribute__((__format__ (__printf__, format_idx, arg_idx)))
#else
#define XPL_PRINTF(a, b)
#endif

#if defined va_copy
#define XPL_VA_COPY(save, ap) va_copy(save, ap)
#elif defined __va_copy
#define XPL_VA_COPY(save, ap) __va_copy(save, ap)
#else 
#warning nothing
#define XPL_VA_COPY(save, ap) save = ap
#endif

/* Limits */
#ifdef PATH_MAX
# define XPL_MAX_PATH PATH_MAX
#else
# error "XPL_MAX_PATH is not implemented on this platform"
#endif

#define MAXEMAILNAMESIZE    256

/* Packing/Byte order */
#define Xpl8BitPackedStructure __attribute__ ((aligned(1),packed))
#define Xpl64BitPackedStructure __attribute__ ((aligned(8),packed))

#if _BONGO_XPL_BIG_ENDIAN
# define XplHostToLittle(LongWord) (((LongWord & 0xFF000000) >>24) | ((LongWord & 0x00FF0000) >> 8) | ((LongWord & 0x0000FF00) << 8) | ((LongWord & 0x000000FF) << 24))
#else
# define XplHostToLittle(n) (n)
#endif
  
/* C Library functions */

# define XplStrCaseCmp(a,b) strcasecmp(a,b)
# define XplStrNCaseCmp(a,b,c) strncasecmp(a,b,c)
# define XplVsnprintf vsnprintf
# define XplGetHighResolutionTimer()   0
# define XplGetHighResolutionTime(counter) {	\
	struct timeval tOfDaYs;			\
	gettimeofday(&tOfDaYs,NULL);		\
	(counter) = (time_t) tOfDaYs.tv_usec;	\
}
/* UI Definitions - need to reconsider these*/

# define XplBell()

# define XplConsolePrintf printf

#ifdef HAVE_UNGETCHARACTER
# define XplUngetCh(a) ungetcharacter((a))
#else
# define XplUngetCh(a)
#endif


/* File and Directory Functions */
int XplTruncate(const char *path, int64_t length);

# define XPL_DIR_SEPARATOR '/'

#define XplFSeek64(FILEPTR, OFFSET, WHENCE)         fseek(FILEPTR, OFFSET, WHENCE)

typedef struct _XplDir {
	unsigned long  d_attr;
	unsigned long  d_size;
	char *d_name;
	unsigned long  d_cdatetime;
	DIR *dirp;
	struct dirent *direntp;
	struct stat stats;
	char Path[XPL_MAX_PATH];
} XplDir;

#define   XplMakeDir(path)                             mkdir(path, S_IRUSR | S_IWUSR | S_IXUSR | S_IRGRP | S_IWGRP)

XplDir   *XplOpenDir(const char  *dirname);
XplDir   *XplReadDir(XplDir *dirp);
int       XplCloseDir(XplDir *dirp);
int       XplRemoveDir(const char *dir);

# define XplFlushWrites(fileptr)		      fsync(fileno(fileptr))

#if defined(_A_SUBDIR)
# define XPL_A_SUBDIR _A_SUBDIR
#elif defined(S_IFDIR)
# define XPL_A_SUBDIR S_IFDIR
#endif

/* Utility Functions */
char *XplStrLower (char *);

FILE *XplOpenTemp(const char *dir,
                  const char *mode,
                  char *filename);


/* Statistics Functions */
unsigned long XplGetMemAvail (void);
long          XplGetServerUtilization (unsigned long *previousSeconds, unsigned long *previousIdle);
unsigned long XplGetDiskspaceUsed(char *Path);
unsigned long XplGetDiskspaceFree(char *Path);
unsigned long XplGetDiskBlocksize(char *Path);

# define XPL_DLL_EXTENSION ".so"

// #ifdef _BONGO_HAVE_DLFCN_H
#include <dlfcn.h>

typedef void *XplPluginHandle;
# define  XplLoadDLL(Path)                                dlopen((Path), RTLD_NOW | RTLD_GLOBAL)
# define  XplGetDLLFunction(Name, Function, Handle)       dlsym((Handle), (Function))
# define  XplReleaseDLLFunction(Name, Function, Handle)
# define  XplUnloadDLL(Name, Handle)                      if (Handle) dlclose((Handle))
# define  XplIsDLLLoaded(Name)										0
# define  FARPROC						 void *

# define XplGetCurrentOSLanguageID() 4

int XplReturnLanguageName(int lang, char *buffer);

/* Time */
# define XplCalendarTime(time) (time)

void XplDelaySelect(int msec);

void XplDelayNanosleep(int msec);
#define XplDelay(msec) XplDelayNanosleep(msec)

/* Signal handling */

#define XplExit(ccode)                          kill(getpid(), 9)
typedef void (*XplShutdownFunc)(int Signal);
void XplSignalCatcher(XplShutdownFunc ShutdownFunction);
void XplSignalBlock(void);
#define XplSignalHandler(handler)								XplSignalCatcher((handler));

#define XplUnloadApp(ID)	kill((ID), SIGKILL)

/* Read/Write locks */
typedef struct {
	long		RWState;
	unsigned long	RWMode;
	unsigned long	RWReaders;
	XplSemaphore	RWLock;
	XplSemaphore	RWMutexRead;
	XplSemaphore	RWMutexReadWrite;
} XplRWLock;

BOOL	XplRWLockInit(XplRWLock *RWLock);
BOOL	XplRWLockDestroy(XplRWLock *RWLock);
BOOL	XplRWReadLockAcquire(XplRWLock *RWLock);
BOOL	XplRWWriteLockAcquire(XplRWLock *RWLock);
BOOL	XplRWReadLockRelease(XplRWLock *RWLock);
BOOL	XplRWWriteLockRelease(XplRWLock *RWLock);

#define s_addr_1	s_addr>>24 & 0xff
#define s_addr_2	s_addr>>16 & 0xff
#define s_addr_3	s_addr>>8  & 0xff
#define s_addr_4        s_addr     & 0xff

#ifndef s_net
#ifdef _BONGO_XPL_LITTLE_ENDIAN

#define s_net	s_addr_4
#define s_host	s_addr_3
#define s_lh	s_addr_2
#define s_impno	s_addr_1

#else

#define s_net   s_addr_1
#define s_host  s_addr_2
#define s_lh    s_addr_3
#define s_impno s_addr_4

#endif
#endif

#ifdef __cplusplus
#define XPL_BEGIN_C_LINKAGE extern "C" {
#else
#define XPL_BEGIN_C_LINKAGE
#endif

#ifdef __cplusplus
#define XPL_END_C_LINKAGE }
#else
#define XPL_END_C_LINKAGE
#endif


#endif
