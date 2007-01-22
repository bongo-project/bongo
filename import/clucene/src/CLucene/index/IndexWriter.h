/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef _lucene_index_IndexWriter_
#define _lucene_index_IndexWriter_

#if defined(_LUCENE_PRAGMA_ONCE)
# pragma once
#endif

#include "CLucene/analysis/AnalysisHeader.h"
#include "CLucene/util/VoidList.h"
#include "CLucene/search/Similarity.h"
#include "CLucene/store/Lock.h"
#include "CLucene/store/TransactionalRAMDirectory.h"

#include "SegmentHeader.h"

CL_NS_DEF(index)

	///	An IndexWriter creates and maintains an index.
	///
	///	The third argument to the <a href="#IndexWriter"><b>constructor</b></a>
	///	determines whether a new index is created, or whether an existing index is
	///	opened for the addition of new documents.
	///
	///	In either case, documents are added with the <a
	///	href="#addDocument"><b>addDocument</b></a> method.  When finished adding
	///	documents, <a href="#close"><b>close</b></a> should be called.
	///
	///	If an index will not have more documents added for a while and optimal search
	///	performance is desired, then the <a href="#optimize"><b>optimize</b></a>
	///	method should be called before the index is closed.
	class IndexWriter:LUCENE_BASE {
	private:
      bool isOpen; //indicates if the writers is open - this way close can be called multiple times

		// how to analyze text
		CL_NS(analysis)::Analyzer* analyzer;

      CL_NS(search)::Similarity* similarity; // how to normalize

      /** Use compound file setting. Defaults to true, minimizing the number of
      * files used.  Setting this to false may improve indexing performance, but
      * may also cause file handle problems.
      */
      bool useCompoundFile;
      bool closeDir;

		CL_NS(store)::TransactionalRAMDirectory* ramDirectory; // for temp segs

		CL_NS(store)::LuceneLock* writeLock;

		void _IndexWriter(const bool create);

		void _finalize();

		// where this index resides
		CL_NS(store)::Directory* directory;		
			
			
		int32_t getSegmentsCounter(){ return segmentInfos->counter; }
		int32_t maxFieldLength;
		int32_t mergeFactor;
		int32_t minMergeDocs;
		int32_t maxMergeDocs;
		
		
	public:
		DEFINE_MUTEX(THIS_LOCK)
		
		// Release the write lock, if needed. 
		SegmentInfos* segmentInfos;
	  
		// Release the write lock, if needed.
		~IndexWriter();

		// The Java implementation of Lucene silently truncates any tokenized
		// field if the number of tokens exceeds a certain threshold.  Although
		// that threshold is adjustable, it is easy for the client programmer
		// to be unaware that such a threshold exists, and to become its
		// unwitting victim.
		// CLucene implements a less insidious truncation policy.  Up to
		// DEFAULT_MAX_FIELD_LENGTH tokens, CLucene behaves just as JLucene
		// does.  If the number of tokens exceeds that threshold without any
		// indication of a truncation preference by the client programmer,
		// CLucene raises an exception, prompting the client programmer to
		// explicitly set a truncation policy by adjusting maxFieldLength.
		LUCENE_STATIC_CONSTANT(int32_t, DEFAULT_MAX_FIELD_LENGTH = 10000);
		LUCENE_STATIC_CONSTANT(int32_t, FIELD_TRUNC_POLICY__WARN = -1);
		int32_t getMaxFieldLength(){ return maxFieldLength; }
		void setMaxFieldLength(int32_t val){ maxFieldLength = val; }

		// Determines how often segment indices are merged by addDocument().  With
		// smaller values, less RAM is used while indexing, and searches on
		// unoptimized indices are faster, but indexing speed is slower.  With larger
		// values more RAM is used while indexing and searches on unoptimized indices
		// are slower, but indexing is faster.  Thus larger values (> 10) are best
		// for batched index creation, and smaller values (< 10) for indices that are
		// interactively maintained.
		//
		// <p>This must never be less than 2.  The default value is 10.
		int32_t getMergeFactor(){ return mergeFactor; }
		void setMergeFactor(int32_t val){ mergeFactor = val; }
      
		/** Determines the minimal number of documents required before the buffered
		* in-memory documents are merging and a new Segment is created.
		* Since Documents are merged in a {@link RAMDirectory},
		* large value gives faster indexing.  At the same time, mergeFactor limits
		* the number of files open in a FSDirectory.
		*
		* <p> The default value is 10.*/
		int32_t getMinMergeDocs(){ return minMergeDocs; }
		void setMinMergeDocs(int32_t val){ minMergeDocs = val; }
    

		// Determines the largest number of documents ever merged by addDocument().
		// Small values (e.g., less than 10,000) are best for interactive indexing,
		// as this limits the length of pauses while indexing to a few seconds.
		// Larger values are best for batched indexing and speedier searches.
		//
		// <p>The default value is {@link Integer#MAX_VALUE}.
		int32_t getMaxMergeDocs(){ return maxMergeDocs; }
		void setMaxMergeDocs(int32_t val){ maxMergeDocs = val; }

		/**
		* Constructs an IndexWriter for the index in <code>path</code>.
		* Text will be analyzed with <code>a</code>.  If <code>create</code>
		* is true, then a new, empty index will be created in
		* <code>path</code>, replacing the index already there, if any.
		*
		* @param path the path to the index directory
		* @param a the analyzer to use
		* @param create <code>true</code> to create the index or overwrite
		*  the existing one; <code>false</code> to append to the existing
		*  index
		* @throws IOException if the directory cannot be read/written to, or
		*  if it does not exist, and <code>create</code> is
		*  <code>false</code>
		*/
		IndexWriter(const char* path, CL_NS(analysis)::Analyzer* a, const bool create, const bool closeDir=true);
		
		
		// Constructs an IndexWriter for the index in <code>d</code>.  Text will be
		//	analyzed with <code>a</code>.  If <code>create</code> is true, then a new,
		//	empty index will be created in <code>d</code>, replacing the index already
		//	there, if any.
		IndexWriter(CL_NS(store)::Directory* d, CL_NS(analysis)::Analyzer* a, const bool create, const bool closeDir=false);

		/**
		* Flushes all changes to an index, closes all associated files, and closes
		* the directory that the index is stored in.
		*/
		void close();

		// Returns the number of documents currently in this index.
		// synchronized
		int32_t docCount();


		/**
      * Adds a document to this index, using the provided analyzer instead of the
      * value of {@link #getAnalyzer()}.  If the document contains more than
      * {@link #maxFieldLength} terms for a given field, the remainder are
      * discarded.
      */
		void addDocument(CL_NS(document)::Document* doc);
      

		// Merges all segments together into a single segment, optimizing an index
		//	for search.
		// synchronized
		void optimize();


		// Merges all segments from an array of indices into this index.
		//
		// <p>This may be used to parallelize batch indexing.  A large document
		// collection can be broken into sub-collections.  Each sub-collection can be
		// indexed in parallel, on a different thread, process or machine.  The
		// complete index can then be created by merging sub-collection indices
		// with this method.
		//
		// <p>After this completes, the index is optimized.
		// synchronized
		void addIndexes(CL_NS(store)::Directory** dirs);
			
  /** Merges the provided indexes into this index.
  * <p>After this completes, the index is optimized. </p>
  * <p>The provided IndexReaders are not closed.</p>
  */
    void addIndexes(IndexReader** readers);


		 /** Returns the directory this index resides in. */
		CL_NS(store)::Directory* getDirectory() { return directory; }

      /** Setting to turn on usage of a compound file. When on, multiple files
      *  for each segment are merged into a single file once the segment creation
      *  is finished. This is done regardless of what directory is in use.
      */
      bool getUseCompoundFile() {
         return useCompoundFile;
      }

      /** Setting to turn on usage of a compound file. When on, multiple files
      *  for each segment are merged into a single file once the segment creation
      *  is finished. This is done regardless of what directory is in use.
      */
      void setUseCompoundFile(bool value) {
         useCompoundFile = value;
      }


      /** Expert: Set the Similarity implementation used by this IndexWriter.
      *
      * @see Similarity#setDefault(Similarity)
      */
      void setSimilarity(CL_NS(search)::Similarity* similarity) {
         this->similarity = similarity;
      }

      /** Expert: Return the Similarity implementation used by this IndexWriter.
      *
      * <p>This defaults to the current value of {@link Similarity#getDefault()}.
      */
      CL_NS(search)::Similarity* getSimilarity() {
         return this->similarity;
      }

      /** Returns the analyzer used by this index. */
      CL_NS(analysis)::Analyzer* getAnalyzer() {
         return analyzer;
      }

		#ifndef LUCENE_HIDE_INTERNAL
			// Some operating systems (e.g. Windows) don't permit a file to be deleted
			// while it is opened for read (e.g. by another process or thread).  So we
			// assume that when a delete fails it is because the file is open in another
			// process, and queue the file for subsequent deletion. 
	      // These functions are public so that the lucenelockwith can access them
			void deleteSegments(CL_NS(util)::CLVector<SegmentReader*>* segments);
			void deleteFiles(CL_NS(util)::AStringArrayConstWithDeletor* files, CL_NS(store)::Directory* directory);
			void deleteFiles(CL_NS(util)::AStringArrayConstWithDeletor* files, CL_NS(util)::AStringArrayConstWithDeletor* deletable);
		#endif
	private:
		
      // Merges all RAM-resident segments.
		void flushRamSegments();

		// Incremental segment merger.
		void maybeMergeSegments();

		// Pops segments off of segmentInfos stack down to minSegment, merges them,
		//	and pushes the merged index onto the top of the segmentInfos stack.
		void mergeSegments(const uint32_t minSegment);

		CL_NS(util)::AStringArrayConstWithDeletor* readDeleteableFiles();
		void writeDeleteableFiles(CL_NS(util)::AStringArrayConstWithDeletor* files);

		// synchronized
		char* newSegmentName();
	};

   ///\exclude
	class IndexWriterLockWith:public CL_NS(store)::LuceneLockWith{
	public:
		IndexWriter* writer;
		bool create;
		void* doBody();
      IndexWriterLockWith(CL_NS(store)::LuceneLock* lock, int64_t lockWaitTimeout, IndexWriter* wr, bool cr):
         CL_NS(store)::LuceneLockWith(lock,lockWaitTimeout)
      {
			this->writer = wr;
			this->create = cr;
		}
	};
   
   ///\exclude
	class IndexWriterLockWith2:public CL_NS(store)::LuceneLockWith{
	public:
		CL_NS(util)::CLVector<SegmentReader*>* segmentsToDelete;
		IndexWriter* writer;
		void* doBody();
      IndexWriterLockWith2(CL_NS(store)::LuceneLock* lock, int64_t lockWaitTimeout,IndexWriter* wr, 
      	 CL_NS(util)::CLVector<SegmentReader*>* std):
         CL_NS(store)::LuceneLockWith(lock,lockWaitTimeout)
      {
			this->writer = wr;
			this->segmentsToDelete = std;
		}
	};

CL_NS_END
#endif
