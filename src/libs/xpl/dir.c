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
#include <xplutil.h>
#include <stdio.h>
#include <ctype.h>

#ifdef NETWARE
XplDir
*XplOpenDir(const char *DirName)
{
	unsigned char	Buffer[XPL_MAX_PATH + 1];

	if (DirName && (strlen(DirName)<(XPL_MAX_PATH-4)))
	{
		strcpy(Buffer, DirName);
		strcat(Buffer, "/*.*");
		return(opendir(Buffer));
	}
	return(NULL);
}
#endif /* NETWARE */

#if defined(LIBC)

#include <sys/stat.h>

XplDir
*XplOpenDir(const char *dirname)
{
	register XplDir *newDir;

	if (dirname) {
		newDir = (XplDir *)malloc(sizeof(XplDir));
		if (newDir != NULL)	{
			newDir->dirp = opendir(dirname);
			if (newDir->dirp != NULL) {
				strcpy(newDir->Path, dirname);

				newDir->d_size = 0;
				newDir->d_attr = newDir->dirp->d_type;
				newDir->d_name = newDir->dirp->d_name;
				newDir->direntp = NULL;

				return(newDir);
			}

			free(newDir);
		}
	}

	return(NULL);
}

XplDir
*XplReadDir(XplDir *dirp)
{
	unsigned char	path[XPL_MAX_PATH + 5];

	if (dirp && dirp->dirp) {
		dirp->direntp = readdir(dirp->dirp);
		if (dirp->direntp) {
			dirp->d_name = dirp->direntp->d_name;

			sprintf(path, "%s/%s", dirp->Path, dirp->direntp->d_name);
			if (stat(path, &(dirp->Stats)) == 0) {
				dirp->d_attr = dirp->Stats.st_mode;
				dirp->d_cdatetime = dirp->Stats.st_mtime;
				dirp->d_size = dirp->Stats.st_size;
			}

			return(dirp);
		}
	}

	return(NULL);
}

int
XplCloseDir(XplDir *dirp)
{
	register int	ccode;

	if (dirp && dirp->dirp) {
		ccode = closedir(dirp->dirp);
		free(dirp);

		return(ccode);
	}

	return(-1);
}

#endif /* defined(LIBC) */

#if defined(SOLARIS) || defined(LINUX) || defined(S390RH)
#include <sys/stat.h>
XplDir
*XplOpenDir(const char *dirname)
{
	XplDir *NewDir	= NULL;

	if (dirname)
	{
		NewDir = (XplDir*)malloc(sizeof(XplDir));
		if (NewDir)
		{
			strcpy(NewDir->Path, dirname);
			NewDir->d_attr	= 0;
			NewDir->d_name	= NULL;
			NewDir->direntp= NULL;
			NewDir->dirp	= opendir(dirname);
			if (NewDir->dirp==NULL) {
				free(NewDir);
				return(NULL);
			}
		}
	}
	return(NewDir);
}

XplDir
*XplReadDir(XplDir *dirp)
{
	if (dirp && dirp->dirp)
	{
		dirp->direntp = readdir(dirp->dirp);
		if (dirp->direntp)
		{
			struct stat		Stats;
			unsigned char	Path[XPL_MAX_PATH+1];

			sprintf(Path, "%s/%s", dirp->Path, dirp->direntp->d_name);
			stat(Path, &Stats);
			dirp->d_attr	   = Stats.st_mode;
			dirp->d_name	   = dirp->direntp->d_name;
			dirp->d_cdatetime = Stats.st_mtime;
			dirp->d_size	   = Stats.st_size;

			return(dirp);
		}
	}
	return(NULL);
}

int
XplCloseDir(XplDir *dirp)
{
	int retval = -1;

	if (dirp && dirp->dirp)
	{
		retval = closedir(dirp->dirp);
		free(dirp);
		dirp = NULL;
	}
	return(retval);
}

char *XplStrLower(char *str)
{
	if (str)
	{
		char *ptr;
		for (ptr = str; *ptr != '\0'; ptr++)
		{
			*ptr = tolower(*ptr);
		}
	}
	return(str);
}


#endif  /**  SOLARIS || LINUX || S390RH  **/

#if defined(WIN32)
XplDir
*XplOpenDir(const char *DirName)
{
	XplDir			*NewDir	= NULL;

	if (DirName) {
		NewDir = (XplDir*)malloc(sizeof(XplDir));
		if (NewDir) {
			if (DirName && (strlen(DirName)<(XPL_MAX_PATH-4))) {
				sprintf(NewDir->Path, "%s/*.*", DirName);
			} else {
				free(NewDir);
				return(NULL);
			}
			NewDir->d_attr	= 0;
			NewDir->d_name	= NULL;
			NewDir->dirp	= -1;
		}
	}
	return(NewDir);
}

XplDir
*XplReadDir(XplDir *dirp)
{
	if (dirp) {
		if (dirp->dirp!=-1) {
			if (_findnext(dirp->dirp, &(dirp->FindData))!=-1) {
				dirp->d_attr	   = dirp->FindData.attrib;
				dirp->d_name	   = dirp->FindData.name;
				dirp->d_cdatetime = dirp->FindData.time_write;
				dirp->d_size	   = dirp->FindData.size;
				return(dirp);
			}
		} else {
			dirp->dirp	= _findfirst(dirp->Path, &(dirp->FindData));
			if (dirp->dirp!=-1) {
				dirp->d_attr	   = dirp->FindData.attrib;
				dirp->d_name	   = dirp->FindData.name;
				dirp->d_cdatetime = dirp->FindData.time_write;
				dirp->d_size	   = dirp->FindData.size;
				return(dirp);
			}
		}
	}
	return(NULL);
}

int
XplCloseDir(XplDir *dirp)
{
	int retval = -1;

	if (dirp && dirp->dirp!=-1) {
		retval = _findclose(dirp->dirp);
		free(dirp);
		dirp = NULL;
	}
	return(retval);
}

#endif
