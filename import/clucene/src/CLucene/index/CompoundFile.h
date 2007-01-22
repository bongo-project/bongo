/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef _lucene_index_compoundfile_h
#define _lucene_index_compoundfile_h

#if defined(_LUCENE_PRAGMA_ONCE)
# pragma once
#endif

#include "CLucene/store/IndexInput.h"
#include "CLucene/store/IndexOutput.h"
#include "CLucene/store/Directory.h"
#include "CLucene/store/Lock.h"
#include "CLucene/util/VoidList.h"
#include "CLucene/util/VoidMap.h"


CL_NS_DEF(index)


/**
 * Class for accessing a compound stream.
 * This class implements a directory, but is limited to only read operations.
 * Directory methods that would normally modify data throw an exception.
 *
 */
class CompoundFileReader: public CL_NS(store)::Directory {

	/** Implementation of an IndexInput that reads from a portion of the
	*  compound file. The visibility is left as "package" *only* because
	*  this helps with testing since JUnit test cases in a different class
	*  can then access package fields of this class.
	*/
	class CSInputStream:public CL_NS(store)::BufferedIndexInput {
	private:
		CL_NS(store)::IndexInput* base;
		int64_t fileOffset;
		int64_t _length;
	protected:
		/** Expert: implements buffer refill.  Reads uint8_ts from the current
		*  position in the input.
		* @param b the array to read uint8_ts into
		* @param offset the offset in the array to start storing uint8_ts
		* @param length the number of uint8_ts to read
		*/
		void readInternal(uint8_t* b, const int32_t offset, const int32_t len);
		void seekInternal(const int64_t pos)
		{
		}

	public:
		CSInputStream(CL_NS(store)::IndexInput* base, const int64_t fileOffset, const int64_t length);
		CSInputStream(const CSInputStream& clone);
		~CSInputStream();

		/** Closes the stream to futher operations. */
		void close();
		CL_NS(store)::IndexInput* clone() const;

		int64_t length(){ return _length; }
	};

	class FileEntry:LUCENE_BASE {
	public:
		int64_t offset;
		int64_t length;
		FileEntry(){
			offset=0;
			length=0;
		}
		~FileEntry(){
		}
	};

private:
	// Base info
	CL_NS(store)::Directory* directory;
	char fileName[CL_MAX_PATH];

	CL_NS(store)::IndexInput* stream;
	CL_NS(util)::CLHashMap<const char*,FileEntry*,
		CL_NS(util)::Compare::Char,
		CL_NS(util)::Deletor::acArray,
		CL_NS(util)::Deletor::Object<FileEntry> > entries;


public:
	CompoundFileReader(CL_NS(store)::Directory* dir, char* name);
	~CompoundFileReader();
	CL_NS(store)::Directory* getDirectory();
	char* getName();

	void close();
	CL_NS(store)::IndexInput* openInput(const char* id);

	/** Returns an array of strings, one for each file in the directory-> */
	char** list() const;
	/** Returns true iff a file with the given name exists. */
	bool fileExists(const char* name) const;
	/** Returns the time the named file was last modified. */
	int64_t fileModified(const char* name) const;
	/** Set the modified time of an existing file to now. */
	void touchFile(const char* name);
	/** Removes an existing file in the directory-> */
	void deleteFile(const char* name, const bool throwError = true);
	/** Renames an existing file in the directory->
	If a file already exists with the new name, then it is replaced.
	This replacement should be atomic. */
	void renameFile(const char* from, const char* to);
	/** Returns the length of a file in the directory */
	int64_t fileLength(const char* name) const;
	/** Creates a new, empty file in the directory with the given name.
	Returns a stream writing this file. */
	CL_NS(store)::IndexOutput* createOutput(const char* name);
	/** Construct a {@link Lock}.
	* @param name the name of the lock file
	*/
	CL_NS(store)::LuceneLock* makeLock(const char* name);

	TCHAR* toString() const;
	
	const char* getDirectoryType() const{ return "CFS"; }
};


/**
 * Combines multiple files into a single compound file.
 * The file format:<br>
 * <ul>
 *     <li>VInt fileCount</li>
 *     <li>{Directory}
 *         fileCount entries with the following structure:</li>
 *         <ul>
 *             <li>int64_t dataOffset</li>
 *             <li>UTFString extension</li>
 *         </ul>
 *     <li>{File Data}
 *         fileCount entries with the raw data of the corresponding file</li>
 * </ul>
 *
 * The fileCount integer indicates how many files are contained in this compound
 * file. The {directory} that follows has that many entries. Each directory entry
 * contains an encoding identifier, an int64_t pointer to the start of this file's
 * data section, and a UTF String with that file's extension.
 *
 */
class CompoundFileWriter:LUCENE_BASE {
	class WriterFileEntry:LUCENE_BASE {
	public:
		WriterFileEntry(){
			directoryOffset=0;
			dataOffset=0;
		}
		~WriterFileEntry(){
		}
		/** source file */
		char file[CL_MAX_PATH];

		/** temporary holder for the start of directory entry for this file */
		int64_t directoryOffset;

		/** temporary holder for the start of this file's data section */
		int64_t dataOffset;

	};

private:
	CL_NS(store)::Directory* directory;
	char fileName[CL_MAX_PATH];
	CL_NS(util)::CLHashMap<char*, void*,
		CL_NS(util)::Compare::Char,CL_NS(util)::Deletor::acArray,
		CL_NS(util)::Deletor::Dummy> ids; //todo: should be CLHashSet (but needs to be searchable)
	CL_NS(util)::CLLinkedList<WriterFileEntry*,
		CL_NS(util)::Deletor::Object<WriterFileEntry> > entries;
	bool merged;
	/** Copy the contents of the file with specified extension into the
	*  provided output stream. Use the provided buffer for moving data
	*  to reduce memory allocation.
	*/
	void copyFile(WriterFileEntry* source, CL_NS(store)::IndexOutput* os, uint8_t* buffer, int32_t bufferLength);
public:
	/** Create the compound stream in the specified file. The file name is the
	*  entire name (no extensions are added).
	*/
	CompoundFileWriter(CL_NS(store)::Directory* dir, char* name);
	~CompoundFileWriter();
	/** Returns the directory of the compound file. */
	CL_NS(store)::Directory* getDirectory();
	char* getName();
	/** Add a source stream. If sourceDir is NULL, it is set to the
	*  same value as the directory where this compound stream exists.
	*  The id is the string by which the sub-stream will be know in the
	*  compound stream. The caller must ensure that the ID is unique. If the
	*  id is NULL, it is set to the name of the source file.
	*/
	void addFile(char* file);
	/** Merge files with the extensions added up to now.
	*  All files with these extensions are combined sequentially into the
	*  compound stream. After successful merge, the source files
	*  are deleted.
	*/
	void close();
};

CL_NS_END
#endif
