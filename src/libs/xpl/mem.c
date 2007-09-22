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

#if defined(SOLARIS) || defined(LINUX) || defined(S390RH)
#include <sys/resource.h>
#include <signal.h>

XplShutdownFunc	ApplicationXplShutdownFunction = NULL;

static void XPLSignalProcessor(int signo, siginfo_t *info, void *context)
{
	if (ApplicationXplShutdownFunction) {
		ApplicationXplShutdownFunction(signo);
	}

    switch (signo) {
        case SIGHUP:
        case SIGUSR2:
        case SIGUSR1:
        case SIGINT:
        case SIGCHLD:
        case SIGTERM:
        case SIGPIPE :
        case SIGPROF: {
            break;        
        }
        default: {
            XplConsolePrintf("SignalDebug: Received %d\n", signo);
        }
    }
	return;
}

unsigned int _XplSignalList[] = { 
    SIGTERM, SIGINT, SIGUSR1, SIGUSR2, SIGHUP, SIGPIPE,
    
    /* debugging - catch these signals too for now */
    SIGALRM, SIGVTALRM,
    SIGCHLD,
#ifdef SIGPOLL
    SIGPOLL,
#endif 
#ifdef SIGSTKFLT
    SIGSTKFLT,
#endif
#ifdef SIGPWR
    SIGPWR,
#endif
#ifdef SIGUNUSED
    SIGUNUSED,
#endif

    0  /* zero used as end-of-array marker, do not remove ;) */
};

void XplSignalBlock(void)
{
    sigset_t signalSet;
    int i;

    sigemptyset(&signalSet);
    
    for (i=0; _XplSignalList[i] != 0; i++) {
        sigaddset(&signalSet, _XplSignalList[i]);
    }

    pthread_sigmask(SIG_BLOCK, &signalSet, NULL);
    return;
}

void XplSignalCatcher(XplShutdownFunc XplShutdownFunction)
{
    sigset_t signalSet;
    struct sigaction act;
    int i;

    XplSignalBlock();

    memset (&act, 0, sizeof(struct sigaction));
    ApplicationXplShutdownFunction = XplShutdownFunction;

    sigemptyset(&act.sa_mask);

    for (i=0; _XplSignalList[i] != 0; i++) {
        sigaddset(&signalSet, _XplSignalList[i]);
    }

    pthread_sigmask(SIG_UNBLOCK, &signalSet, NULL);

    act.sa_sigaction = XPLSignalProcessor;
    
    for (i=0; _XplSignalList[i] != 0; i++) {
        if (sigaction(_XplSignalList[i], &act, NULL) != 0) {
            perror("XplSignalCatcher()");
        }
    }

    return;
}

                                                                  
unsigned long
XplGetMemAvail(void)
{
	struct rlimit	rlp;

#if defined(RLIMIT_AS)
	getrlimit(RLIMIT_AS, &rlp);
#elif defined(RLIMIT_DATA)
	getrlimit(RLIMIT_DATA, &rlp);
#elif defined(RLIMIT_VMEM)
	getrlimit(RLIMIT_VMEM, &rlp);
#else
#error XplGetMemAvail not ported.
#endif
	return(rlp.rlim_cur);
}

#endif

const unsigned char CAN[] = { 'F', 'r', 'a', 'n', 0xC3, 0xA7, 'a', 'i', 's', ' ', 'C', 'A', 'N', 0 };
const unsigned char CHS[] = { 0xE7, 0xAE, 0x80, 0xE4, 0xBD, 0x93, 0xE4, 
										0xB8, 0xAD, 0xE6, 0x96, 0x87, 0x20, ' ', 'C', 'H', 'S', 0 };

const unsigned char FRA[] = { 'F', 'r', 'a', 'n', 0xC3, 0xA7, 'a', 'i', 's', ' ', 'F', 'R', 'A', 0 };
const unsigned char JPN[] = { 0xE6, 0x97, 0xA5, 0xE6, 0x9C, 0xAC, 0xE8, 0xAA, 0x9E, ' ', 'J', 'P', 'N', 0};
const unsigned char KOR[] = { 0xED, 0x95, 0x9C, 0xEA, 0xB5, 0xAD, 0xEC, 0x96, 0xB4, ' ', 'K', 'O', 'R', 0};
const unsigned char BRA[] = { 0x50, 0x6F, 0x72, 0x74, 0x75,  0x67, 0x75, 0xC3, 0xAA, 0x73, 0x20, 
										0x64, 0x6F,  0x20, 0x42, 0x72, 0x61, 0x73, 0x69, 0x6C, ' ', 'P', 'T', 'B', 0};
const unsigned char RUS[] = { 0xD1, 0x80, 0xD1, 0x83, 0xD1,  0x81, 0xD1, 0x81, 0xD0, 0xBA, 0xD0, 
										0xB8, 0xD0, 0xB9, ' ', 'R', 'U', 'S', 0};
const unsigned char SA[] =  { 0x45, 0x73, 0x70, 0x61, 0xC3,  0xB1, 0x6F, 0x6C, ' ', 'L', 'A', 'T', 0};
const unsigned char CHT[] = { 0xE7, 0xB9, 0x81, 0xE9, 0xAB,  0x94, 0xE4, 0xB8, 0xAD, 0xE6, 0x96, 0x87, ' ', 'C', 'H', 'T', 0};
const unsigned char POR[] = { 0x50, 0x6F, 0x72, 0x74, 0x75,  0x67, 0x75, 0xC3, 0xAA, 0x73, ' ', 'P', 'O', 'R', 0};
const unsigned char SPA[] = { 0x45, 0x73, 0x70, 0x61, 0xC3,  0xB1, 0x6F, 0x6C, ' ', 'S', 'P', 'A', 0};
const unsigned char CZE[] = { 0xC4, 0x8D, 0x65, 0x73, 0x6B, 0x79, ' ', 'C', 'Z', 'E', 0};
const unsigned char THA[] = { 0xE0, 0xB9, 0x82, 0xE0, 0xB8,  0x94, 0xE0, 0xB8, 0xA2, ' ', 'T', 'H', 'A', 0};
const unsigned char TUR[] = { 0x54, 0xC3, 0xBC, 0x72, 0x6B,  0xC3, 0xA7, 0x65, ' ', 'T', 'U', 'R', 0};
const unsigned char HEB[] = { 0xD7, 0xA2, 0xD7, 0x91, 0xD7,  0xA8, 0xD7, 0x99, 0xD7, 0xAA, ' ', 'H', 'E', 'B', 0};
const unsigned char ARA[] = { 0xEF, 0xBB, 0x8B, 0xEF, 0xBA, 0xAD, 0xEF, 0xBA,  0x91, 0xEF, 0xBB, 0xB2, ' ', 'A', 'R', 'A', 0};

const unsigned char GRC[] = { 'G', 'R', 'E', 'E', 'K', ' ', 'G', 'R', 'C', 0};

int
XplReturnLanguageName(int lang, unsigned char *Buffer)
{
	switch(lang) {
		case 0: memcpy(Buffer, CAN, strlen(CAN)+1); break;
		case 1: memcpy(Buffer, CHS, strlen(CHS)+1); break;
		case 2: strcpy(Buffer, "Dansk DAN"); break;
		case 3: strcpy(Buffer, "Nederlands NDL"); break;
		case 4: strcpy(Buffer, "English"); break;
		case 5: strcpy(Buffer, "Suomi FIN"); break;
		case 6: memcpy(Buffer, FRA, strlen(FRA)+1); break;
		case 7: strcpy(Buffer, "Deutsch DEU"); break;
		case 8: strcpy(Buffer, "Italiano ITA"); break;
		case 9: memcpy(Buffer, JPN, strlen(JPN)+1); break;
		case 10: memcpy(Buffer, KOR, strlen(KOR)+1); break;
		case 11: strcpy(Buffer, "Norsk NOR"); break;
		case 12: memcpy(Buffer, BRA, strlen(BRA)+1); break;
		case 13: memcpy(Buffer, RUS, strlen(RUS)+1); break;
		case 14: memcpy(Buffer, SA,  strlen(SA)+1);  break;
		case 15: strcpy(Buffer, "Svenska SVE"); break;
		case 16: memcpy(Buffer, CHT,  strlen(CHT)+1);  break;
		case 17: strcpy(Buffer, "Polski POL"); break;
		case 18: memcpy(Buffer, POR,  strlen(POR)+1);  break;
		case 19: memcpy(Buffer, SPA,  strlen(SPA)+1);  break;
		case 20: strcpy(Buffer, "Magyar HUN"); break;
		case 21: memcpy(Buffer, CZE,  strlen(CZE)+1);  break;
		case 22: memcpy(Buffer, THA,  strlen(THA)+1);  break;
		case 29: strcpy(Buffer, "Lietuvių LT"); break;
		case 26: memcpy(Buffer, GRC,  strlen(GRC)+1);  break;
		case 41: memcpy(Buffer, TUR,  strlen(TUR)+1);  break;
		case 60:	memcpy(Buffer, HEB,  strlen(HEB)+1);  break;
		case 61: memcpy(Buffer, ARA,  strlen(ARA)+1);  break;
		default:	
			sprintf(Buffer, "Unknown (ID: %d)", lang); 
			break;
	}
	return(0);
}

#if defined(WIN32)

BOOL
XPLGetNullDACL(SECURITY_ATTRIBUTES *sa)
{
	PSECURITY_DESCRIPTOR		pHandleSD = NULL;

	memset ((void *)sa, 0, sizeof(SECURITY_ATTRIBUTES));

	pHandleSD = (PSECURITY_DESCRIPTOR)(malloc(SECURITY_DESCRIPTOR_MIN_LENGTH));
	if (!pHandleSD) {
		return(FALSE);
	}

	if (!InitializeSecurityDescriptor(pHandleSD, SECURITY_DESCRIPTOR_REVISION)) {
		return(FALSE);
	}

	// set NULL DACL on the SD
	if (!SetSecurityDescriptorDacl(pHandleSD, TRUE, (PACL) NULL, FALSE)) {
		return(FALSE);
	}

	// now set up the security attributes
	sa->nLength = sizeof(SECURITY_ATTRIBUTES);
	sa->bInheritHandle = TRUE; 
	sa->lpSecurityDescriptor = pHandleSD;

	return(TRUE);
}


XplPluginHandle
XplLoadDLL(unsigned char *Name)
{
	unsigned char	CopyBuf[XPL_MAX_PATH];
	unsigned char	*ptr; 

	strcpy(CopyBuf, Name); 
	ptr=CopyBuf; 
	while(*ptr) {
		if (*ptr=='/') {
			*ptr='\\'; 
		}
		ptr++;
	}
	return(LoadLibrary(CopyBuf));
}


BOOL
XplIsDLLLoaded(unsigned char *Name)
{
	unsigned char	CopyBuf[XPL_MAX_PATH];
	unsigned char	*ptr; 

	strcpy(CopyBuf, Name); 
	ptr=CopyBuf; 
	while(*ptr) {
		if (*ptr=='/') {
			*ptr='\\'; 
		}
		ptr++;
	}
	return(GetModuleHandle(CopyBuf)!=NULL);
}

int
XplGetCurrentOSLanguageID(void)
{
	/* Should build something around GetLocaleInfo() */
	return(4);
}

unsigned long
XplGetMemAvail(void)
{
    MEMORYSTATUS	MemStat;

	GlobalMemoryStatus(&MemStat);
	return(MemStat.dwAvailPhys);
}

void
XplDebugOut(const char *Format, ...)
{
	unsigned char	DebugBuffer[10240];
	va_list	argptr;

	va_start(argptr, Format);	
	vsprintf(DebugBuffer, Format, argptr);
	va_end(argptr);

	OutputDebugString(DebugBuffer);
}

#endif /* WIN32 */

#if defined(LIBC)
unsigned long 
XplGetMemAvail(void)
{
	struct memory_info	info;

	if (netware_mem_info(&info) == 0) {
		return(info.CacheBufferMemory);
	}

	return(0);
}
#endif	/*	defined(LIBC)	*/
