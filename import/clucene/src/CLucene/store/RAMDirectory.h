/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef _lucene_store_RAMDirectory_
#define _lucene_store_RAMDirectory_

#if defined(_LUCENE_PRAGMA_ONCE)
# pragma once
#endif

#include "Lock.h"
#include "Directory.h"
#include "CLucene/util/VoidMap.h"
#include "CLucene/util/Arrays.h"

CL_NS_DEF(store)

	class RAMFile:LUCENE_BASE {
	public:
		CL_NS(util)::CLVector<uint8_t*,CL_NS(util)::Deletor::Array<uint8_t> > buffers;
		int64_t length;
		uint64_t lastModified;

        #ifdef _DEBUG
		const char* filename;
        #endif

		RAMFile();
		~RAMFile();
	};

	class RAMIndexOutput: public IndexOutput {
	protected:
		RAMFile* file;
		int32_t pointer;
		bool deleteFile;
	public:

		RAMIndexOutput(RAMFile* f);
		RAMIndexOutput();
  	    /** Construct an empty output buffer. */
		virtual ~RAMIndexOutput();

		// output methods: 
		void flushBuffer(const uint8_t* src, const int32_t len);

		virtual void close();

		// Random-at methods
		virtual void seek(const int64_t pos);
		int64_t Length();
        /** Resets this to an empty buffer. */
        void reset();
  	    /** Copy the current contents of this buffer to the named output. */
        void writeTo(IndexOutput* output);
	};

	class RAMIndexInput:public BufferedIndexInput {
	private:
		RAMFile* file;
		int32_t pointer;
		RAMIndexInput(const RAMIndexInput& clone);
		int64_t _length;
	public:
		RAMIndexInput(RAMFile* f);
		~RAMIndexInput();
		IndexInput* clone() const;

		/** IndexInput methods */
		/** DSR:CL_BUG:
		** The CLucene 0.8.11 implementation of readInternal caused invalid memory
		** ops if fewer than the requested number of bytes were available and the
		** difference between requested and available happened to cross a buffer
		** boundary.
		** A bufferNumber would then be generated for a buffer that did not exist,
		** so an invalid buffer pointer was fetched from beyond the end of the
		** file->buffers vector, and an attempt was made to read bytes from the
		** bad buffer pointer.  Often this resulted in reading uninitialized memory;
		** it only segfaulted occasionally.
		** I replaced the dysfunctional CLucene 0.8.11 impl with a fixed version. */
		void readInternal(uint8_t *dest, const int32_t idestOffset, const int32_t len);

		void close();

        /** Random-at methods */
		void seekInternal(const int64_t pos);

		int64_t length();
	};


   /**
   * A memory-resident {@link Directory} implementation.
   */
	class RAMDirectory:public Directory{

		class RAMLock: public LuceneLock{
		private:
			RAMDirectory* directory;
			const char* fname;
		public:
			RAMLock(const char* name, RAMDirectory* dir);
			virtual ~RAMLock();
			bool obtain();
			void release();
			bool isLocked();
			virtual TCHAR* toString();
		};

		typedef CL_NS(util)::CLHashMap<const char*,RAMFile*, 
				CL_NS(util)::Compare::Char,CL_NS(util)::Deletor::acArray ,
				CL_NS(util)::Deletor::Object<RAMFile> > FileMap;
	protected:
		
  /**
   * Creates a new <code>RAMDirectory</code> instance from a different
   * <code>Directory</code> implementation.  This can be used to load
   * a disk-based index into memory.
   * <P>
   * This should be used only with indices that can fit into memory.
   *
   * @param dir a <code>Directory</code> value
   * @exception IOException if an error occurs
   */
	  void _copyFromDir(Directory* dir, bool closeDir);
	  FileMap files; // unlike the java Hashtable, FileMap is not synchronized, and all access must be protected by a lock
	public:
#ifndef _CL_DISABLE_MULTITHREADING //do this so that the mutable keyword still works without mt enabled
	    mutable DEFINE_MUTEX(files_mutex) // mutable: const methods must also be able to synchronize properly
#endif

		/// Returns a null terminated array of strings, one for each file in the directory. 
		char** list() const;

      /** Constructs an empty {@link Directory}. */
		RAMDirectory();
		
	  ///Destructor - only call this if you are sure the directory
	  ///is not being used anymore. Otherwise use the ref-counting
	  ///facilities of dir->close
		virtual ~RAMDirectory();
		RAMDirectory(Directory* dir);
		
	  /**
	   * Creates a new <code>RAMDirectory</code> instance from the {@link FSDirectory}.
	   *
	   * @param dir a <code>String</code> specifying the full index directory path
	   */
		RAMDirectory(const char* dir);
		       
		/// Returns true iff the named file exists in this directory. 
		bool fileExists(const char* name) const;

		/// Returns the time the named file was last modified. 
		int64_t fileModified(const char* name) const;

		/// Returns the length in bytes of a file in the directory. 
		int64_t fileLength(const char* name) const;

		/// Removes an existing file in the directory. 
		virtual void renameFile(const char* from, const char* to);

		/// Removes an existing file in the directory. 
		virtual void deleteFile(const char* name, const bool throwError = true);

		/** Set the modified time of an existing file to now. */
		void touchFile(const char* name);

		/// Creates a new, empty file in the directory with the given name.
		///	Returns a stream writing this file. 
		virtual IndexOutput* createOutput(const char* name);

		/// Construct a {@link Lock}.
		/// @param name the name of the lock file
		LuceneLock* makeLock(const char* name);

		/// Returns a stream reading an existing file. 
		IndexInput* openInput(const char* name);

		virtual void close();
		
		TCHAR* toString() const;

	    const char* getDirectoryType() const{ return "RAM"; }
	};
CL_NS_END
#endif
