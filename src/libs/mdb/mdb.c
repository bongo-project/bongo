/*
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
 */

/* We use bongo-specific keys/dirs here rather than mdb, so that we don't conflict before 
 * we merge */

#include <config.h>
#include <xpl.h>
#include <errno.h>
#include <signal.h>

/* Local modification, bongo's xpl no longer defines these */

#include <mdb.h>
#include "mdbp.h"

/* Globals */
LONG				Tid;
LONG				TGid;
XplSemaphore 	ShutdownSemaphore;
BOOL				MDBInitialized			= FALSE;
BOOL				MDBLoading				= FALSE;
BOOL                            MDBFailed = FALSE;


#define	ChopNL(String)		{ unsigned char *pTr; pTr=strchr((String), 0x0a);	if (pTr)	*pTr='\0'; pTr=strrchr((String), 0x0d); if (pTr) *pTr='\0'; }

#if defined(NETWARE)
#define	MDB_CONFIG_INIT
#define	MDB_CONFIG_FILE		"sys:/etc/mdb.cfg"
unsigned char	DriverDir[XPL_MAX_PATH+1]	= "sys:\\system";
#elif defined(WIN32)
#define	MDB_CONFIG_INIT		{																																			\
											HKEY HKey;																															\
											GetWindowsDirectory(MDB_CONFIG_FILE, sizeof(MDB_CONFIG_FILE)); 													\
											strcat(MDB_CONFIG_FILE, "/mdb.cfg");																						\
																																													\
											if (RegOpenKeyEx(HKEY_LOCAL_MACHINE, "Software\\Bongo\\MDB", 0, KEY_READ, &HKey)==ERROR_SUCCESS) {	\
												DWORD		BufSize=XPL_MAX_PATH;																								\
												RegQueryValueEx(HKey, "DriverDir", NULL, NULL, DriverDir, &BufSize);													\
												RegCloseKey(HKey);																											\
											}																																		\
										}

unsigned char	MDB_CONFIG_FILE[256+1];
unsigned char	DriverDir[XPL_MAX_PATH+1]	= "C:\\program files\\Bongo\\MDB";
#elif defined(SUSE) || defined(LINUX) || defined(S390RH) || defined(REDHAT) || defined(SOLARIS) || defined(MACOSX)
#define	MDB_CONFIG_INIT
#define	MDB_CONFIG_FILE		XPL_DEFAULT_CONF_DIR "/mdb.conf"
unsigned char	DriverDir[XPL_MAX_PATH+1]	= DRIVER_DIR;
#endif

typedef struct _MDBDriverStruct {
	unsigned char					Driver[256];
	BOOL								Primary;
	MDBDriverInitFunc				Init;
	MDBDriverShutdownFunc		Shutdown;
	MDBInterfaceStruct			Interface;
	XplPluginHandle				Handle;
	void								*Data;
} MDBDriverStruct;

typedef struct {
	unsigned char	Module[XPL_MAX_PATH];
	unsigned char	Driver[XPL_MAX_PATH];
	unsigned char	Arguments[512];
} MDBModuleStruct;

unsigned long		MDBDriverCount		= 0;
MDBDriverStruct	*MDBDrivers			= NULL;

unsigned long		MDBModuleCount		= 0;
unsigned long		MDBModuleAlloc		= 0;
MDBModuleStruct	*MDBModules			= NULL;

unsigned long		MDBDefaultDriver	= 0;

static unsigned long
MDBFindDriver(const unsigned char *Driver)
{
	unsigned long	i;

	for (i=0; i<MDBDriverCount; i++) {
		if (XplStrCaseCmp(MDBDrivers[i].Driver, Driver)==0) {
			return(i);
		}
	}
	return(-1);
}

static XplPluginHandle
MDBGetDriverHandle(const unsigned char *Driver)
{
	unsigned long	i;

	for (i=0; i<MDBDriverCount; i++) {
		if (XplStrCaseCmp(MDBDrivers[i].Driver, Driver)==0) {
			return(MDBDrivers[i].Handle);
		}
	}
	return(NULL);
}

static unsigned long
MDBInitDriver (MDBDriverStruct *Driver,
	       const unsigned char *Arguments,
	       unsigned char *Error)

{
	unsigned char FuncName[XPL_MAX_PATH+sizeof ("Shutdown")];
	MDBDriverInitStruct Init;

	strcpy (FuncName, Driver->Driver);
	strcat (FuncName, "Init");
	
	/* FIXME: what do we do about DriverName here */
	Driver->Init=(MDBDriverInitFunc)XplGetDLLFunction(DriverName, FuncName, Driver->Handle);

	if (!Driver->Init) {
		if (Error) sprintf(Error, "Driver %s, missing Init function", Driver->Driver);
		if (Driver->Primary) {
			XplUnloadDLL(Driver->Driver, Driver->Handle);
		}
		return(-1);
	}

	/* Grab the shutdown function */
	strcpy(FuncName, Driver->Driver);
	strcat(FuncName, "Shutdown");

	Driver->Shutdown=(MDBDriverShutdownFunc)XplGetDLLFunction(DriverName, FuncName, Driver->Handle);
	if (!Driver->Shutdown) {
      if (Error) sprintf(Error, "Driver %s, missing Shutdown function", Driver->Driver);
		if (Driver->Primary) {
			XplUnloadDLL(Driver->Driver, Driver->Handle);
		}
		return(-1);
	}

	memset(&Init, 0, sizeof(Init));
	Init.Arguments=Arguments;

	if (!Driver->Init(&Init)) {
		if (Error) sprintf(Error, "Driver %s, Init failed with: %s", Driver->Driver, Init.Error);
		Driver->Shutdown();
		/* Can't unload for now; dclient creates a bunch of threads and they go to hell if we unload this dll */
		//XplUnloadDLL(Driver->Driver, Driver->Handle);
		return(-1);
	}
	memcpy(&Driver->Interface, &Init.Interface, sizeof(MDBInterfaceStruct));
	return 0;
}



static unsigned long
MDBLoadDriver(const unsigned char *DriverNameIn, const unsigned char *Arguments, unsigned char *Error)
{
	unsigned char			DriverName[XPL_MAX_PATH+1];
	unsigned char                   Name[XPL_MAX_PATH+1];
	
	unsigned char			*ptr;
	MDBDriverStruct		        *Driver;

	/* First, make sure there's enough space in our list */
	MDBDrivers=realloc(MDBDrivers, sizeof(MDBDriverStruct)*(MDBDriverCount+1));
	if (!MDBDrivers) {
		return(-1);
	}
	Driver=&MDBDrivers[MDBDriverCount];
	memset(Driver, 0, sizeof(MDBDriverStruct));

	strcpy(DriverName, DriverNameIn);

	ptr=DriverName;
	while (*ptr) {
		*ptr=tolower(*ptr);
		ptr++;
	}

TryAgain:
	if ((strchr(DriverNameIn, '/')==NULL) && (strchr(DriverNameIn, '\\')==NULL)) {
		if ((strlen(DriverName)<5) || (XplStrNCaseCmp(DriverName+strlen(DriverName)-strlen(DRIVER_EXTENSION), DRIVER_EXTENSION, strlen(DRIVER_EXTENSION))!=0)) {
			ptr=strrchr(DriverName, '.');
			if (ptr && XplStrNCaseCmp(ptr,".NLM", 4)==0) {
				strcpy(ptr, DRIVER_EXTENSION);
				goto TryAgain;
			} else {
				/* No period means possibly no extension, let's add our default extension */
				strcat(DriverName, DRIVER_EXTENSION);
				goto TryAgain;
			}
			if (Error) strcpy(Error, "Bad driver extension");
			return(-1);
		}
	} else {
		unsigned char	*ptr;

		if ((ptr=strrchr(DriverNameIn, '/'))==NULL) {
			ptr=strrchr(DriverNameIn, '\\');
		}
		ptr++;
		strcpy(DriverName, ptr);
	}

	strcpy(Name, DriverName);
	Name[strlen(Name)-strlen(DRIVER_EXTENSION)]='\0';
	ptr=Name;
	while (*ptr) {
		*ptr=toupper(*ptr);
		ptr++;
	}

	Driver->Handle=MDBGetDriverHandle(DriverName);	   

	if (!Driver->Handle) {
		if (!(strchr(DriverNameIn, '/')==NULL) && (strchr(DriverNameIn, '\\')==NULL)) { 
                        /* Use the absolute path if it was passed */
			Driver->Handle=XplLoadDLL(DriverNameIn);
		} else {
			
			/* Otherwise try <name> then lib<name> */
			unsigned char Path[XPL_MAX_PATH + 1];
			
			sprintf(Path, "%s%c%s", DriverDir, XPL_DIR_SEPARATOR, DriverName);
			
			Driver->Handle=XplLoadDLL(Path);
			if (!Driver->Handle) {
				sprintf(Path, "%s%clib%s", DriverDir, XPL_DIR_SEPARATOR, DriverName);
				Driver->Handle=XplLoadDLL(Path);
				
			}
		}
		Driver->Primary=TRUE;
	}

	if (!Driver->Handle) {
		if (Error) sprintf(Error, "Could not load driver, error %d", errno);
		return(-1);
	}

	/* Give the module a chance to initialize */

	strcpy(Driver->Driver, Name);
	if (MDBInitDriver (Driver, Arguments, Error) == 0) {
		
	    //XplConsolePrintf("MDBDriver '%s', successfully loaded\n", DriverName);
		return(MDBDriverCount++);
	} else {
		return(-1);
	}
}

EXPORT int
MDBGetAPIVersion(BOOL WantCompatibleVersion, unsigned char *Description, MDBHandle Context)
{
	if (Description) {
		if (Context) {
			strncpy(Description, ((MDBContextCacheStruct *)Context)->Description, MDB_MAX_API_DESCRIPTION_CHARS-1);
		} else {
			strcpy(Description, "<undefined>");
		}
	}

	if (WantCompatibleVersion) {
		return(MDB_COMPATIBLE_TO_API);
	} else {
		return(MDB_CURRENT_API_VERSION);
	}
}

EXPORT BOOL
MDBGetServerInfo(unsigned char *HostDN, unsigned char *HostTree, MDBValueStruct *V)
{
	if (!V) {
		return(MDBDrivers[MDBDefaultDriver].Interface.MDBGetServerInfo(HostDN, HostTree, V));
	} else {
		return(V->Interface->MDBGetServerInfo(HostDN, HostTree, V));
	}
}

EXPORT char *
MDBGetBaseDN(MDBValueStruct *V)
{
	if (!V) {
		return(MDBDrivers[MDBDefaultDriver].Interface.MDBGetBaseDN(V));
	} else {
		return(V->Interface->MDBGetBaseDN(V));
	}
}


/* Authentication */
EXPORT MDBHandle
MDBAuthenticate(const unsigned char *Module, const unsigned char *Object, const unsigned char *Password)
{
	unsigned int				i;
	unsigned long	Driver=MDBDefaultDriver;
	MDBHandle		Handle;
	unsigned char	*Arguments=NULL;

	for (i=0; i<MDBModuleCount; i++) {
		if (XplStrCaseCmp(Module, MDBModules[i].Module)==0) {
			Driver=MDBFindDriver(MDBModules[i].Driver);
			if (Driver!=(unsigned long)-1) {
				Arguments=MDBModules[Driver].Arguments;
			} else {
				Driver=MDBDefaultDriver;
			}
		}
	}

	Handle=MDBDrivers[Driver].Interface.MDBAuthenticate(Object, Password, Arguments);
	if (Handle) {
		((MDBContextCacheStruct *)Handle)->Interface=&MDBDrivers[Driver].Interface;
	}
	return(Handle);
}


EXPORT BOOL
MDBRelease(MDBHandle Handle)
{
	if (Handle) {
		return(((MDBContextCacheStruct *)Handle)->Interface->MDBRelease(Handle));
	} else {
		return(FALSE);
	}
}


EXPORT MDBValueStruct
*MDBCreateValueStruct(MDBHandle Handle, const unsigned char *Context)
{
	MDBValueStruct *V;

	V=((MDBContextCacheStruct *)Handle)->Interface->MDBCreateValueStruct(Handle, Context);

	if (V) {
		V->Interface=((MDBContextCacheStruct *)Handle)->Interface;
	}
	return(V);
}


EXPORT BOOL
MDBDestroyValueStruct(MDBValueStruct *V)
{
	return(V->Interface->MDBDestroyValueStruct(V));
}


EXPORT MDBValueStruct
*MDBShareContext(MDBValueStruct *VOld)
{
	MDBValueStruct	*VNew;

	VNew=VOld->Interface->MDBShareContext(VOld);
	if (VNew) {
		VNew->Interface=VOld->Interface;
	}
	return(VNew);
}


EXPORT BOOL
MDBSetValueStructContext(const unsigned char *Context, MDBValueStruct *V)
{
	return(V->Interface->MDBSetValueStructContext(Context, V));
}


EXPORT MDBEnumStruct
*MDBCreateEnumStruct(MDBValueStruct *V)
{
	return(V->Interface->MDBCreateEnumStruct(V));
}


EXPORT BOOL
MDBDestroyEnumStruct(MDBEnumStruct *E, MDBValueStruct *V)
{
	return(V->Interface->MDBDestroyEnumStruct(E, V));
}


EXPORT BOOL
MDBAddValue(const unsigned char *Value, MDBValueStruct *V)
{
	return(V->Interface->MDBAddValue(Value, V));
}


EXPORT BOOL
MDBFreeValue(unsigned long Index, MDBValueStruct *V)
{
	return(V->Interface->MDBFreeValue(Index, V));
}


EXPORT BOOL
MDBFreeValues(MDBValueStruct *V)
{
	return(V->Interface->MDBFreeValues(V));
}


EXPORT long
MDBRead(const unsigned char *Object, const unsigned char *Attribute, MDBValueStruct *V)
{
	return(V->Interface->MDBRead(Object, Attribute, V));
}


EXPORT const unsigned char
*MDBReadEx(const unsigned char *Object, const unsigned char *Attribute, MDBEnumStruct *E, MDBValueStruct *V)
{
	return(V->Interface->MDBReadEx(Object, Attribute, E, V));
}


EXPORT long
MDBReadDN(const unsigned char *Object, const unsigned char *Attribute, MDBValueStruct *V)
{
	return(V->Interface->MDBReadDN(Object, Attribute, V));
}


EXPORT BOOL
MDBWriteTyped(const unsigned char *Object, const unsigned char *Attribute, const int AttrType, MDBValueStruct *V)
{
	return(V->Interface->MDBWriteTyped(Object, Attribute, AttrType, V));
}

EXPORT BOOL
MDBWrite(const unsigned char *Object, const unsigned char *Attribute, MDBValueStruct *V)
{
	return(V->Interface->MDBWrite(Object, Attribute, V));
}


EXPORT BOOL
MDBWriteDN(const unsigned char *Object, const unsigned char *Attribute, MDBValueStruct *V)
{
	return(V->Interface->MDBWriteDN(Object, Attribute, V));
}


EXPORT BOOL
MDBClear(const unsigned char *Object, const unsigned char *Attribute, MDBValueStruct *V)
{
	return(V->Interface->MDBClear(Object, Attribute, V));
}


EXPORT BOOL
MDBAdd(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *Value, MDBValueStruct *V)
{
	return(V->Interface->MDBAdd(Object, Attribute, Value, V));
}


EXPORT BOOL
MDBAddDN(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *Value, MDBValueStruct *V)
{
	return(V->Interface->MDBAddDN(Object, Attribute, Value, V));
}


EXPORT BOOL
MDBRemove(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *Value, MDBValueStruct *V)
{
	return(V->Interface->MDBRemove(Object, Attribute, Value, V));
}


EXPORT BOOL
MDBRemoveDN(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *Value, MDBValueStruct *V)
{
	return(V->Interface->MDBRemoveDN(Object, Attribute, Value, V));
}


EXPORT BOOL
MDBIsObject(const unsigned char *Object, MDBValueStruct *V)
{
	return(V->Interface->MDBIsObject(Object, V));
}


EXPORT BOOL
MDBGetObjectDetails(const unsigned char *Object, unsigned char *Type, unsigned char *RDN, unsigned char *DN, MDBValueStruct *V)
{
	return(V->Interface->MDBGetObjectDetails(Object, Type, RDN, DN, V));
}

EXPORT BOOL
MDBGetObjectDetailsEx(const unsigned char *Object, MDBValueStruct *Types, unsigned char *RDN, unsigned char *DN, MDBValueStruct *V)
{
	return(V->Interface->MDBGetObjectDetailsEx(Object, Types, RDN, DN, V));
}

EXPORT BOOL
MDBVerifyPassword(const unsigned char *Object, const unsigned char *Password, MDBValueStruct *V)
{
	return(V->Interface->MDBVerifyPassword(Object, Password, V));
}


EXPORT BOOL
MDBVerifyPasswordEx(const unsigned char *Object, const unsigned char *Password, MDBValueStruct *V)
{
	return(V->Interface->MDBVerifyPasswordEx(Object, Password, V));
}


EXPORT BOOL
MDBChangePassword(const unsigned char *Object, const unsigned char *OldPassword, const unsigned char *NewPassword, MDBValueStruct *V)
{
	return(V->Interface->MDBChangePassword(Object, OldPassword, NewPassword, V));
}


EXPORT BOOL
MDBChangePasswordEx(const unsigned char *Object, const unsigned char *OldPassword, const unsigned char *NewPassword, MDBValueStruct *V)
{
	return(V->Interface->MDBChangePasswordEx(Object, OldPassword, NewPassword, V));
}


EXPORT long
MDBEnumerateObjects(const unsigned char *Container, const unsigned char *Type, const unsigned char *Pattern, MDBValueStruct *V)
{
	return(V->Interface->MDBEnumerateObjects(Container, Type, Pattern, V));
}


EXPORT const unsigned char
*MDBEnumerateObjectsEx(const unsigned char *Container, const unsigned char *Type, const unsigned char *Pattern, unsigned long Flags, MDBEnumStruct *E, MDBValueStruct *V)
{
	return(V->Interface->MDBEnumerateObjectsEx(Container, Type, Pattern, Flags, E, V));
}


EXPORT const unsigned char	
*MDBEnumerateAttributesEx(const unsigned char *Object, MDBEnumStruct *E, MDBValueStruct *V)
{
	return(V->Interface->MDBEnumerateAttributesEx(Object, E, V));
}

EXPORT BOOL
MDBCreateAlias(const unsigned char *Alias, const unsigned char *AliasedObjectDn, MDBValueStruct *V)
{
	return(V->Interface->MDBCreateAlias(Alias, AliasedObjectDn, V));
}

EXPORT BOOL
MDBCreateObject(const unsigned char *Object, const unsigned char *Class, MDBValueStruct *Attribute, MDBValueStruct *Data, MDBValueStruct *V)
{
	return(V->Interface->MDBCreateObject(Object, Class, Attribute, Data, V));
}


EXPORT BOOL
MDBDeleteObject(const unsigned char *Object, BOOL Recursive, MDBValueStruct *V)
{
	return(V->Interface->MDBDeleteObject(Object, Recursive, V));
}


EXPORT BOOL
MDBRenameObject(const unsigned char *ObjectOld, const unsigned char *ObjectNew, MDBValueStruct *V)
{
	return(V->Interface->MDBRenameObject(ObjectOld, ObjectNew, V));
}


EXPORT BOOL
MDBDefineAttribute(const unsigned char *Attribute, const unsigned char *ASN1, unsigned long Type, BOOL SingleValue, BOOL ImmediateSync, BOOL Public, MDBValueStruct *V)
{
	return(V->Interface->MDBDefineAttribute(Attribute, ASN1, Type, SingleValue, ImmediateSync, Public, V));
}


EXPORT BOOL
MDBUndefineAttribute(const unsigned char *Attribute, MDBValueStruct *V)
{
	return(V->Interface->MDBUndefineAttribute(Attribute, V));
}


EXPORT BOOL
MDBDefineClass(const unsigned char *Class, const unsigned char *ASN1, BOOL Container, MDBValueStruct *Superclass, MDBValueStruct *Containment, MDBValueStruct *Naming, MDBValueStruct *Mandatory, MDBValueStruct *Optional, MDBValueStruct *V)
{
	return(V->Interface->MDBDefineClass(Class, ASN1, Container, Superclass, Containment, Naming, Mandatory, Optional, V));
}


EXPORT BOOL
MDBAddAttribute(const unsigned char *Attribute, const unsigned char *Class, MDBValueStruct *V)
{
	return(V->Interface->MDBAddAttribute(Attribute, Class, V));
}


EXPORT BOOL
MDBUndefineClass(const unsigned char *Class, MDBValueStruct *V)
{
	return(V->Interface->MDBUndefineClass(Class, V));
}


EXPORT BOOL
MDBListContainableClasses(const unsigned char *Container, MDBValueStruct *V)
{
	return(V->Interface->MDBListContainableClasses(Container, V));
}


EXPORT const unsigned char
*MDBListContainableClassesEx(const unsigned char *Container, MDBEnumStruct *E, MDBValueStruct *V)
{
	return(V->Interface->MDBListContainableClassesEx(Container, E, V));
}


EXPORT BOOL
MDBGrantObjectRights(const unsigned char *Object, const unsigned char *TrusteeDN, BOOL Read, BOOL Write, BOOL Delete, BOOL Rename, BOOL Admin, MDBValueStruct *V)
{
	return(V->Interface->MDBGrantObjectRights(Object, TrusteeDN, Read, Write, Delete, Rename, Admin, V));
}


EXPORT BOOL
MDBGrantAttributeRights(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *TrusteeDN, BOOL Read, BOOL Write, BOOL Admin, MDBValueStruct *V)
{
	return(V->Interface->MDBGrantAttributeRights(Object, Attribute, TrusteeDN, Read, Write, Admin, V));
}

EXPORT BOOL
MDBFindObjects(const unsigned char *Container,
               MDBValueStruct *Types,
               const unsigned char *Pattern,
               MDBValueStruct *Attributes,
               int depth, 
               int max,
               MDBValueStruct *V)
{
    if (V->Interface->MDBFindObjects) {
        return(V->Interface->MDBFindObjects(Container, Types, Pattern, Attributes, depth, max, V));
    } else {
        return FALSE;
    }
}

EXPORT BOOL
MDBDumpValueStruct(MDBValueStruct *V)
{
	unsigned long	i;

	if (!V->Used) {
		XplConsolePrintf("No data in structure\n");
	}
	XplConsolePrintf("ValueStruct: Used:%lu\n", V->Used);
	for (i=0; i<V->Used; i++) {
		XplConsolePrintf("Value[%02lu] %-25s", i, V->Value[i]);
		if (((i+1) % 2)==0) {
			XplConsolePrintf("\n");
		} else {
			XplConsolePrintf(" | ");
		}
	}
	XplConsolePrintf("\r\n");
	return(TRUE);
}


/*
	Config format:

	DriverDir=<Directory>
	Default=<Drivername>
	Driver=<Drivername> [DriverArguments]
	Module <Modulename>=<Drivername> [DriverArguments]
*/

static BOOL
MDBReadConfiguration(void)
{
	FILE				*Handle;
	unsigned char	Buffer[1024];
	unsigned char	Error[XPL_MAX_PATH*2];
	unsigned char	DefaultDriverName[XPL_MAX_PATH+1] = "";

	MDB_CONFIG_INIT;
	Handle=fopen(MDB_CONFIG_FILE, "rb");
	if (!Handle) {
		return(FALSE);
	}

	while (!ferror(Handle) && !feof(Handle)) {
		if (fgets(Buffer, sizeof(Buffer), Handle)!=NULL) {
			ChopNL(Buffer);
			if (XplStrNCaseCmp(Buffer, "DriverDir=", 10)==0) {
				strcpy(DriverDir, Buffer+10);
			} else if (XplStrNCaseCmp(Buffer, "Module ", 7)==0) {
				unsigned char	*Module;
				unsigned char	*Driver;
				unsigned char	*Arguments;

				if ((MDBModuleCount+1)>MDBModuleAlloc) {
					MDBModules=realloc(MDBModules, sizeof(MDBModuleStruct)*(MDBModuleAlloc+10));
					if (!MDBModules) {
						fclose(Handle);
						return(FALSE);
					}
					MDBModuleAlloc+=10;
				}

				Module=Buffer+7;
				Driver=strchr(Module, '=');
				if (Driver) {
					*Driver='\0';
					Driver++;
					if ((Arguments=strchr(Driver, ' '))!=NULL) {
						*Arguments='\0';
						Arguments++;
					}
					strcpy(MDBModules[MDBModuleCount].Module, Module);
					strcpy(MDBModules[MDBModuleCount].Driver, Driver);
					if (Arguments) {
						strcpy(MDBModules[MDBModuleCount].Arguments, Arguments);
					} else {
						MDBModules[MDBModuleCount].Arguments[0]='\0';
					}
					MDBModuleCount++;
				}
			} else if (XplStrNCaseCmp(Buffer, "Driver=", 7)==0) {
				unsigned char	*Arguments;

				if ((Arguments=strchr(Buffer+7, ' '))!=NULL) {
					*Arguments='\0';
					Arguments++;
				}

				if (MDBLoadDriver(Buffer+7, Arguments, Error)==(unsigned long)-1) {
					XplConsolePrintf("\rDriver %s failed to load:%s\n", Buffer+7, Error);
                                        fclose(Handle);
                                        return(FALSE);
				}
			} else if (XplStrNCaseCmp(Buffer, "Default=", 8)==0) {
				strcpy(DefaultDriverName, Buffer+8);
			}
		}
	}
	fclose(Handle);

	if (DefaultDriverName[0]!='\0') {
		MDBDefaultDriver=MDBFindDriver(DefaultDriverName);
	}

	return(TRUE);
}

EXPORT BOOL
MDBInit(void)
{
    if (MDBFailed) {
        return FALSE;
    }
    
    if (!MDBInitialized && !MDBLoading) {
		MDBLoading=TRUE;
		/* Read configuration */
		if (!MDBReadConfiguration()) {
			unsigned char	Error[XPL_MAX_PATH*2];

			if ((MDBLoadDriver("MDBDS", NULL, Error)==(unsigned long)-1) && (MDBLoadDriver("MDBREG", NULL, Error)==(unsigned long)-1) && (MDBLoadDriver("MDBFILE", NULL, Error)==(unsigned long)-1)) {
                            MDBLoading = FALSE;
                            MDBFailed = TRUE;
				XplConsolePrintf("Failed to load any MDB driver\n");

				/* INSERT "fake" MDB driver here */
				return(FALSE);
			}
		}
		
		MDBInitialized=TRUE;
	}

	while (!MDBInitialized) {
		XplDelay(100);
	}

	return(MDBInitialized);
}


EXPORT BOOL
MDBShutdown(void)
{
	return(TRUE);
}

#if defined(NETWARE)
void
MDBSigHandler(int Signal)
{
	int				OldTGid;
	unsigned long	i;

	OldTGid=XplSetThreadGroupID(TGid);

	XplSignalLocalSemaphore(ShutdownSemaphore);
	XplWaitOnLocalSemaphore(ShutdownSemaphore);

	/* Do any required cleanup */
	for (i=0; i<MDBDriverCount; i++) {
		MDBDrivers[i].Shutdown();
		XplUnloadDLL(MDBDrivers[i].Driver, MDBDrivers[i].Handle);
	}
	if (MDBDrivers) {
		free(MDBDrivers);
	}
	if (MDBModules) {
		free(MDBModules);
	}

	XplCloseLocalSemaphore(ShutdownSemaphore);
	XplSetThreadGroupID(OldTGid);
}


int
main(int argc, char *argv[])
{
	Tid=XplGetThreadID();
	TGid=XplGetThreadGroupID();

	signal(SIGTERM, MDBSigHandler);

	/*
		This will "park" the module 'til we get unloaded; 
		it would not be neccessary to do this on NetWare, 
		but to prevent from automatically exiting on Unix
		we need to keep main around...
	*/

	MDBInit();

	XplOpenLocalSemaphore(ShutdownSemaphore, 0);
	XplWaitOnLocalSemaphore(ShutdownSemaphore);
	XplSignalLocalSemaphore(ShutdownSemaphore);
	return(0);
}
#endif


#if defined(WIN32)
BOOL WINAPI
DllMain(HINSTANCE hInst, DWORD Reason, LPVOID Reserved)
{
	if (Reason==DLL_PROCESS_ATTACH) {
		DisableThreadLibraryCalls(hInst);
	}

	return(TRUE);
}
#endif
