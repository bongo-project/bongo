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

#ifndef XPLSERVICE_H
#define XPLSERVICE_H

#ifdef WIN32
# define XplServiceMain                                          XplMain
# define XplStartMainThread(name, id, func, stack, arg, ret)     XplBeginThread((id), (func), (stack), (arg), (ret))

# define XplServiceExit(retcode)                                                      \
   DestroyWindow(XplServiceMainWindowHandle); PostQuitMessage(retcode); return(0);
# define XplServiceCode(XplServiceTerminate)																								\
int		XplMain(int argc, char *argv[]);																								\
EXPORT LRESULT CALLBACK																																\
XplServiceWndProc(HWND hwnd, UINT msg, WPARAM wParam, LPARAM lParam)		 														\
{																																							\
	if (msg==(WM_USER+4321)) {																														\
		DestroyWindow(hwnd);																															\
		PostQuitMessage(0);																															\
		return(0);																																		\
	}																																						\
	return(DefWindowProc(hwnd, msg, wParam, lParam));																						\
}																																							\
																																							\
HWND				XplServiceMainWindowHandle;																									\
int WINAPI																																				\
WinMain(HINSTANCE hInst, HINSTANCE hPrevInstance, LPSTR CommandLine, int nCmdShow)												\
{																																							\
	WNDCLASS			wndclass;																														\
	MSG				msg;																																\
	unsigned char	AppName[128];																													\
	unsigned char	*ptr;																																\
	unsigned char	*argv[20];																														\
	unsigned char	argc=0;																															\
	BOOL				All=FALSE;																														\
	unsigned char	*CmdLine=NULL;																													\
																																							\
	strcpy(AppName, PRODUCT_SHORT_NAME);																										\
	ptr=AppName;																																		\
	while (ptr[0] && ptr[0]!='.') {																												\
		ptr[0]=tolower(ptr[0]);																														\
		ptr++;																																			\
	}																																						\
	if (ptr[0]=='.') {																																\
		ptr[0]='\0';																																	\
	}																																						\
																																							\
	if (FindWindow("XplServiceClass", AppName)) {																							\
		/* Don't start */																																\
		MessageBox(NULL, "Agent already running.", AppName, MB_OK|MB_ICONWARNING);													\
		return(0);																																		\
	}																																						\
																																							\
	/* Build the command line */																													\
																																							\
	if (CommandLine && CommandLine[0]) {																										\
		unsigned char	*ptr;																															\
																																							\
		CmdLine=strdup(CommandLine);																												\
																																							\
		ptr=CmdLine;																																	\
		argv[argc++]=PRODUCT_SHORT_NAME;																											\
		argv[argc++]=ptr;																																\
		while (!All) {																																	\
			switch (ptr[0]) {																															\
				case '"': {																																\
					ptr++;																																\
					argv[argc-1]++;																													\
																																							\
					while (ptr[0] && ptr[0]!='"') {																								\
						ptr++;																															\
					}																																		\
					if (ptr[0]) {																														\
						ptr[0]='\0';																													\
						ptr++;																															\
						while (isspace(ptr[0])) {																									\
							ptr++;																														\
						}																																	\
					}																																		\
					argv[argc++]=ptr;																													\
					break;																																\
				}																																			\
																																							\
				case '\0': {																															\
					All=TRUE;																															\
					break;																																\
				}																																			\
																																							\
				case ' ': {																																\
					ptr[0]='\0';																														\
					ptr++;																																\
					while (isspace(ptr[0])) {																										\
						ptr++;																															\
					}																																		\
					argv[argc++]=ptr;																													\
					break;																																\
				}																																			\
																																							\
				default: {																																\
					ptr++;																																\
					break;																																\
				}																																			\
			}																																				\
		}																																					\
	}																																						\
																																							\
																																							\
	wndclass.style=CS_HREDRAW | CS_VREDRAW;																									\
	wndclass.lpfnWndProc=XplServiceWndProc;																									\
	wndclass.cbClsExtra=0;																															\
	wndclass.cbWndExtra=0;																															\
	wndclass.hInstance=hInst;																														\
	wndclass.hIcon=NULL;																																\
	wndclass.hCursor=NULL;																															\
	wndclass.hbrBackground=NULL;																													\
	wndclass.lpszMenuName=NULL;																													\
	wndclass.lpszClassName="XplServiceClass";																								\
																																							\
	RegisterClass(&wndclass);																														\
																																							\
	XplServiceMainWindowHandle=CreateWindow("XplServiceClass", AppName, WS_OVERLAPPED, 0,0,0,0, NULL, NULL, hInst, NULL);				\
																																							\
	XplServiceMain(argc, argv);																													\
																																							\
	while (GetMessage(&msg, NULL, 0, 0)) {																										\
		if (!IsDialogMessage(XplServiceMainWindowHandle, &msg)) {																		\
			TranslateMessage(&msg);																													\
			DispatchMessage(&msg);																													\
		}																																				 	\
	}																																						\
	_NonAppCheckUnload();																															\
	XplServiceTerminate(SIGTERM);																												\
	UnregisterClass("XplServiceClass", hInst);																								\
	if (CmdLine) {																																		\
		free(CmdLine);																																	\
	}																																						\
	return(msg.wParam);																																\
}


#else

#include <pthread.h>
#include <sys/types.h>

# define XplServiceMain                                              main
# define XplStartMainThread(name, id, startfunc, stack, arg, ret)    (ret) = 0; startfunc(arg)
# define XplServiceCode(shutdownfunc)
# define XplServiceExit(retcode)				     return(retcode)

#endif

#ifdef WIN32
#define XplLookupUser(username, uid, guid) (-1)
#define XplSetEffectiveUser(username) 0
#define XplSetEffectiveUserId(uid)    0
#define XplSEtEffectiveGroupId(gid)   0
#define XplSetRealUser(username)      0
#define XplSetRealUserId(uid)         0
#define XplSetRealGroupId(gid)        0
#else

int XplLookupUser(const char *username, uid_t *uid, gid_t *gid);
int XplSetEffectiveUser(const char *username);
int XplSetEffectiveUserId(uid_t uid);
int XplSetEffectiveGroupId(gid_t gid);

int XplSetRealUser(const char *username);
int XplSetRealUserId(uid_t uid);
int XplSetRealGroupId(gid_t gid);
#endif

#endif
