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

#include "bongo-config.h"
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

#ifdef _BONGO_HAVE_SYS_STAT_H
#include <sys/stat.h>
#endif

#ifdef _BONGO_LINUX
# include <dirent.h>
# include <netdb.h>
# include <netinet/in.h>
# include <sys/poll.h>
# include <sys/socket.h>
# include <sys/time.h>
# include <sys/types.h>
# include <arpa/inet.h>
# include <unistd.h>
#elif defined WIN32
# include <windows.h>
# include <io.h>
#endif

/* Product Definitions */

#define	PRODUCT_MAJOR_VERSION	3
#define	PRODUCT_MINOR_VERSION	52
#define	PRODUCT_LETTER_VERSION	0

/* Basic Definitions */

typedef int BOOL;

#ifndef WIN32
typedef unsigned long LONG;
#endif

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

/* Casts */
#define XPL_PTR_TO_INT(p)      ((int) XPL_POINTER_TO_INT_CAST (p))
#define XPL_PTR_TO_UINT(p)     ((unsigned int) XPL_POINTER_TO_UINT_CAST (p))
#define XPL_INT_TO_PTR(i)      ((void*) XPL_POINTER_TO_INT_CAST (i))
#define XPL_UINT_TO_PTR(i)     ((void*) XPL_POINTER_TO_UINT_CAST (i))

/* Branch hints */
#if defined(__GNUC__) && (__GNUC__ > 2)
#define XPL_LIKELY(e) (__builtin_expect(!!(e), 1))
#define XPL_UNLIKELY(e) (__builtin_expect(!!(e), 0))
#else
#define XPL_LIKELY(e) (e)
#define XPL_UNLIKELY(e) (e)
#endif

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
#elif defined (XPL_MAX_PATH)
# define XPL_MAX_PATH XPL_MAX_PATH
#elif defined (_PC_PATH_MAX)
# define XPL_MAX_PATH sysconf(_PC_PATH_MAX)
#elif defined (_MAX_PATH)
# define XPL_MAX_PATH _MAX_PATH
#else
# error "XPL_MAX_PATH is not implemented on this platform"
#endif

#define MAXEMAILNAMESIZE    256

/* Packing/Byte order */
#ifdef _BONGO_LINUX
# define Xpl8BitPackedStructure __attribute__ ((aligned(1),packed))
# define Xpl64BitPackedStructure __attribute__ ((aligned(8),packed))
#elif defined WIN32
# define Xpl8BitPackedStructure
# define Xpl64BitPackedStructure
#elif defined(NETWARE) || defined(LIBC)
# define Xpl8BitPackedStructure
# define Xpl64BitPackedStructure
#else
# error "Packing not defined on this platform"
#endif

#if _BONGO_XPL_BIG_ENDIAN
# define XplHostToLittle(LongWord) (((LongWord & 0xFF000000) >>24) | ((LongWord & 0x00FF0000) >> 8) | ((LongWord & 0x0000FF00) << 8) | ((LongWord & 0x000000FF) << 24))
#else
# define XplHostToLittle(n) (n)
#endif
  
/* C Library functions */

#ifdef _BONGO_HAVE_STRCASECMP
# define XplStrCaseCmp(a,b) strcasecmp(a,b)
#elif defined(HAVE_STRICMP)
# define XplStrCaseCmp(a,b) stricmp(a,b)
#else
# error "XplStrCaseCmp is not implemented on this platform"
#endif

#ifdef _BONGO_HAVE_STRNCASECMP
# define XplStrNCaseCmp(a,b,c) strncasecmp(a,b,c)
#elif defined(HAVE_STRNICMP)
# define XplStrNCaseCmp(a,b,c) strnicmp(a,b,c)
#else
# error "XplStrNCaseCmp is not implemented on this platform"
#endif

#ifdef _BONGO_HAVE_VSNPRINTF
# define XplVsnprintf vsnprintf
#elif defined (HAVE__VSNPRINTF)
# define XplVsnprintf _vsnprintf
#endif

#ifdef _BONGO_HAVE_GETTIMEOFDAY
# define XplGetHighResolutionTimer()   0
# define XplGetHighResolutionTime(counter)						{	struct timeval	tOfDaYs;					  \
																				gettimeofday(&tOfDaYs,NULL);						\
																				(counter) = (time_t) tOfDaYs.tv_usec;	\
                                                                }
#elif defined (WIN32)


# define XplGetHighResolutionTimer() 0
# define XplGetHighResolutionTime(counter) {	LARGE_INTEGER	c;									\
																		if (QueryPerformanceCounter(&c) != 0) {	\
																			(counter) = c.LowPart;						\
																		}														\
															}
#else
# error "XplGetHighResolutionTime is not implemented on this platform"
#endif

/* UI Definitions - need to reconsider these*/

#ifdef _BONGO_HAVE_RINGTHEBELL
# define XplBell() XplBell()
#else
# define XplBell()
#endif

#if defined WIN32 && (defined DEBUG || defined _DEBUG)

void XplDebugOut(const char *Format, ...);
# define  XplConsolePrintf						XplDebugOut
#elif defined _BONGO_HAVE_CONSOLEPRINTF
# define XplConsolePrintf consoleprintf
#else
# define XplConsolePrintf printf
#endif

#ifdef HAVE_UNGETCHARACTER
# define XplUngetCh(a) ungetcharacter((a))
#else
# define XplUngetCh(a)
#endif


/* File and Directory Functions */
int XplTruncate(const char *path, int64_t length);

#if defined (WIN32) || defined(NETWARE)
# define XPL_DIR_SEPARATOR '\\'
#else
# define XPL_DIR_SEPARATOR '/'
#endif

#ifdef _BONGO_LINUX

#define XplFSeek64(FILEPTR, OFFSET, WHENCE)         fseek(FILEPTR, OFFSET, WHENCE)

typedef struct _XplDir {
	unsigned long  d_attr;
	unsigned long  d_size;
	unsigned char *d_name;
	unsigned long  d_cdatetime;
	DIR *dirp;
	struct dirent *direntp;
	unsigned char Path[XPL_MAX_PATH];
} XplDir;

#define   XplMakeDir(path)                             mkdir(path, S_IRUSR | S_IWUSR | S_IXUSR | S_IRGRP | S_IWGRP)

#elif defined WIN32

#include <direct.h>

#define XplFSeek64(FILEPTR, OFFSET, WHENCE)         ((int)(!((_lseeki64(fileno(FILEPTR), OFFSET, WHENCE)) > -1)))

typedef struct _XplDir {
	unsigned long			d_attr;
	unsigned long			d_size;
	unsigned char			*d_name;
	unsigned long			d_cdatetime;
	long						dirp;  
	struct _finddata_t	FindData;
	unsigned char			Path[_MAX_PATH+1];
} XplDir;


#define	XplMakeDir(path)											mkdir(path)

#endif

XplDir   *XplOpenDir(const char  *dirname);
XplDir   *XplReadDir(XplDir *dirp);
int       XplCloseDir(XplDir *dirp);

#ifdef _BONGO_HAVE_FSYNC
# define XplFlushWrites(fileptr)		      fsync(fileno(fileptr))
#elif defined (_BONGO_HAVE_FEFLUSHWRITE)
# define XplFlushWrites(fileptr)                      FEFlushWrite(fileno(fileptr))
#elif defined (HAVE__COMMIT)
# define XplFlushWrites(fileptr)                      _commit(fileno(fileptr))
#else
# error "XplFlushWrites not defined for this platform"
#endif

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
unsigned long XplGetDiskspaceUsed(unsigned char *Path);
unsigned long XplGetDiskspaceFree(unsigned char *Path);
unsigned long XplGetDiskBlocksize(unsigned char *Path);

/* Shared Library functions */
/* rough cut */

#ifdef WIN32
# define XPL_DLL_EXTENSION ".dll"
#elif defined (NETWARE)
# define XPL_DLL_EXTENSION ".nlm"
#else
# define XPL_DLL_EXTENSION ".so"
#endif

#ifdef _BONGO_HAVE_DLFCN_H
#include <dlfcn.h>
							      
typedef void *XplPluginHandle;
# define  XplLoadDLL(Path)                                dlopen((Path), RTLD_NOW | RTLD_GLOBAL)
# define  XplGetDLLFunction(Name, Function, Handle)       dlsym((Handle), (Function))
# define  XplReleaseDLLFunction(Name, Function, Handle)
# define  XplUnloadDLL(Name, Handle)                      if (Handle) dlclose((Handle))
# define	XplIsDLLLoaded(Name)										0
# define	FARPROC						 void *
#elif defined WIN32

typedef HINSTANCE XplPluginHandle;

XplPluginHandle XplLoadDLL(unsigned char *Path);			/* Defined in libxpl */
BOOL		    XplIsDLLLoaded(unsigned char *Name);				/* Defined in libxpl */

#define	XplGetDLLFunction(Name, Function, Handle)			GetProcAddress((Handle), (Function))
#define	XplReleaseDLLFunction(Name, Function, Handle)	
#define	XplUnloadDLL(Name, Handle)							FreeLibrary(Handle);

#endif

/* Internationalization */
#ifdef WIN32
int XplGetCurrentOSLangaugeID(void);
#else
# define XplGetCurrentOSLanguageID() 4
#endif

int XplReturnLanguageName(int lang, unsigned char *buffer);

/* Time */
#ifdef _BONGO_HAVE__CONVERTDOSTIMETOCALENDAR
# define XplCalendarTime(time) _ConvertDOSTimeToCalendar(time)
#elif defined (HAVE_DOS2CALENDAR)
# define XplCalendarTime(time) dos2calendar(time)
#else
# define XplCalendarTime(time) (time)
#endif

void XplDelaySelect(int msec);

#ifdef _BONGO_HAVE_DELAY
# define XplDelay(msec) delay(msec)
#elif defined (_BONGO_HAVE_SLEEP)
# define XplDelay(msec) Sleep(msec)
#elif defined (_BONGO_HAVE_NANOSLEEP)
void XplDelayNanosleep(int msec);
# define XplDelay(msec) XplDelayNanosleep(msec)
#elif defined (_BONGO_HAVE_USLEEP)
# define XplDelay(msec) usleep((msec) * 1000)
#else
# define  XplDelay(msec) XplDelaySelect(msec)
#endif

/* Signal handling */

#ifdef WIN32
#define XplExit(ccode)                          exit(ccode)
#define XplSignalHandler(handler)				signal(SIGTERM, (handler));
#define XplSignalBlock() 

#else

#define XplExit(ccode)                          kill(getpid(), 9)
typedef void (*XplShutdownFunc)(int Signal);
void XplSignalCatcher(XplShutdownFunc ShutdownFunction);
void XplSignalBlock(void);
#define XplSignalHandler(handler)								XplSignalCatcher((handler));

#endif


#ifdef WIN32
#define XplUnloadApp(id)
#else
#define XplUnloadApp(ID)											kill((ID), SIGKILL)
#endif
/* Read/Write locks */
typedef struct {
	long				RWState;
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

#ifdef _BONGO_LINUX

#define s_addr_1	s_addr>>24 & 0xff
#define s_addr_2	s_addr>>16 & 0xff
#define s_addr_3	s_addr>>8  & 0xff
#define s_addr_4        s_addr     & 0xff

#ifndef s_net
#if _BONGO_XPL_LITTLE_ENDIAN

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
#endif

/* Windows specifics */

#if defined(BONGO_HAVE__SNPRINTF) && !defined(_BONGO_HAVE_SNPRINTF)
#define snprintf _snprintf
#endif

#ifndef _BONGO_HAVE_GMTIME_R
#define	gmtime_r(in, out)			{	struct tm *tmp; tmp=gmtime((time_t *)in); if (!tmp) {	time_t t=1;	tmp=gmtime(&t);}	memcpy(out, tmp, sizeof(struct tm)); }
#endif

#ifndef _BONGO_HAVE_LOCALTIME_R
#define	localtime_r(in, out)		{	struct tm *tmp; tmp=localtime((time_t *)in); if (!tmp) {	time_t t=1;	tmp=localtime(&t);}	memcpy(out, tmp, sizeof(struct tm)); }
#endif

#ifdef WIN32
# define        WIN_CDECL   __cdecl
# define        WIN_STDCALL __stdcall
# define        EXPORT      __declspec(dllexport)
# define        IMPORT      __declspec(dllimport)
#else
# define	WIN_CDECL									
# define	WIN_STDCALL									
# define	EXPORT
# define	IMPORT
#endif

#ifndef ECONNABORTED
#define ECONNABORTED 130
#endif

#ifndef SIGHUP
/* Dummy */
#define SIGHUP 1000
#endif

#ifndef SIGUSR1
/* Dummy */
#define SIGUSR1 10
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
