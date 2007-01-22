/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef _lucene_store_Directory
#define _lucene_store_Directory

#if defined(_LUCENE_PRAGMA_ONCE)
# pragma once
#endif

#include "CLucene/store/Lock.h"
#include "CLucene/util/VoidList.h"
#include "CLucene/util/Misc.h"

#include "IndexInput.h"
#include "IndexOutput.h"

CL_NS_DEF(store)

   /** A Directory is a flat list of files.  Files may be written once, when they
   * are created.  Once a file is created it may only be opened for read, or
   * deleted.  Random access is permitted both when reading and writing.
   *
   * <p> Direct i/o is not used directly, but rather all i/o is
   * through this API.  This permits things such as: <ul>
   * <li> implementation of RAM-based indices;
   * <li> implementation indices stored in a database, via a database;
   * <li> implementation of an index as a single file;
   * </ul>
   *
   * @author Ben van Klinken
   */
	class Directory: LUCENE_REFBASE {
	protected:
		Directory(){
		}
	public:
		DEFINE_MUTEX(THIS_LOCK)
	   
		virtual ~Directory(){ };

		// Returns an null terminated array of strings, one for each file in the directory. 
		virtual char** list() const = 0;
		       
		// Returns true iff a file with the given name exists. 
		virtual bool fileExists(const char* name) const = 0;

		// Returns the time the named file was last modified. 
		virtual int64_t fileModified(const char* name) const = 0;

		// Returns the length of a file in the directory. 
		virtual int64_t fileLength(const char* name) const = 0;

		// Returns a stream reading an existing file. 
		virtual IndexInput* openInput(const char* name) = 0;
		virtual IndexInput* openInput(const char* name, int32_t bufferSize){ return openInput(name); }

		/// Set the modified time of an existing file to now. */
		virtual void touchFile(const char* name) = 0;

		// Removes an existing file in the directory. 
		virtual void deleteFile(const char* name, const bool throwError = true) = 0;

		// Renames an existing file in the directory.
		//	If a file already exists with the new name, then it is replaced.
		//	This replacement should be atomic. 
		virtual void renameFile(const char* from, const char* to) = 0;

		// Creates a new, empty file in the directory with the given name.
		//	Returns a stream writing this file. 
		virtual IndexOutput* createOutput(const char* name) = 0;

		// Construct a {@link Lock}.
		// @param name the name of the lock file
		virtual LuceneLock* makeLock(const char* name) = 0;

		// Closes the store. 
		virtual void close() = 0;
		
		virtual TCHAR* toString() const = 0;

		virtual const char* getDirectoryType() const = 0;
	};
CL_NS_END
#endif


