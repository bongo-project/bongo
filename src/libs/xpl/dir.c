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
#include <sys/stat.h>
#include <memmgr.h>

XplDir
*XplOpenDir(const char *dirname)
{
	XplDir *newDir;

	if (! dirname) return NULL;

	newDir = MemNew0(XplDir, 1);

	newDir->dirp = opendir(dirname);
	if (newDir->dirp == NULL) {
		MemFree(newDir);
		return NULL;
	}
	
	strcpy(newDir->Path, dirname);

	newDir->d_size = 0;
	newDir->d_attr = 0;
	newDir->d_name = NULL;
	newDir->direntp = NULL;

	return(newDir);
}

XplDir
*XplReadDir(XplDir *dirp)
{
	char path[XPL_MAX_PATH + 5];

	if (dirp && dirp->dirp) {
		dirp->direntp = readdir(dirp->dirp);
		if (dirp->direntp) {
			dirp->d_name = dirp->direntp->d_name;

			sprintf(path, "%s/%s", dirp->Path, dirp->direntp->d_name);
			if (stat(path, &(dirp->stats)) == 0) {
				dirp->d_attr = dirp->stats.st_mode;
				dirp->d_cdatetime = dirp->stats.st_mtime;
				dirp->d_size = dirp->stats.st_size;
			}

			return(dirp);
		}
	}

	return(NULL);
}

int
XplCloseDir(XplDir *dirp)
{
	int ccode;

	if (dirp && dirp->dirp) {
		ccode = closedir(dirp->dirp);
		free(dirp);

		return(ccode);
	}

	return(-1);
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
