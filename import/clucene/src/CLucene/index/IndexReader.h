/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef _lucene_index_IndexReader_
#define _lucene_index_IndexReader_

#if defined(_LUCENE_PRAGMA_ONCE)
# pragma once
#endif

#include "CLucene/store/Directory.h"
#include "CLucene/store/FSDirectory.h"
#include "CLucene/store/Lock.h"
#include "CLucene/document/Document.h"
#include "CLucene/index/TermVector.h"
#include "SegmentInfos.h"
#include "Terms.h"


CL_NS_DEF(index)


/** IndexReader is an abstract class, providing an interface for accessing an
 index.  Search of an index is done entirely through this abstract interface,
 so that any subclass which implements it is searchable.

 <p> Concrete subclasses of IndexReader are usually constructed with a call to
 the static method {@link #open}.

 <p> For efficiency, in this API documents are often referred to via
 <i>document numbers</i>, non-negative integers which each name a unique
 document in the index.  These document numbers are ephemeral--they may change
 as documents are added to and deleted from an index.  Clients should thus not
 rely on a given document having the same number between sessions.

*/
	class IndexReader :LUCENE_BASE{
	public:
		//Callback for classes that need to know if IndexReader is closing.
		typedef void (*CloseCallback)(IndexReader*, void*);

		class CloseCallbackCompare:public CL_NS(util)::Compare::_base{
		public:
			bool operator()( CloseCallback t1, CloseCallback t2 ) const{
				return t1 > t2;
			}
			static void doDelete(CloseCallback dummy){
			}
		};

	private:
		CL_NS(store)::LuceneLock* writeLock;

        bool directoryOwner;
        bool stale;
        bool hasChanges;
        bool closeDirectory;

        CL_NS(store)::Directory* directory;
		typedef CL_NS(util)::CLSet<CloseCallback, void*, 
			CloseCallbackCompare,
			CloseCallbackCompare> CloseCallbackMap;
		CloseCallbackMap closeCallbacks;
	  
        /**
        * Trys to acquire the WriteLock on this directory.
        * this method is only valid if this IndexReader is directory owner.
        * 
        * @throws IOException If WriteLock cannot be acquired.
        */
        void aquireWriteLock();
	protected:
        /**
        * Constructor used if IndexReader is not owner of its directory. 
        * This is used for IndexReaders that are used within other IndexReaders that take care or locking directories.
        * 
        * @param directory Directory where IndexReader files reside.
        */
	    IndexReader(CL_NS(store)::Directory* dir);

	    /**
        * Constructor used if IndexReader is owner of its directory.
        * If IndexReader is owner of its directory, it locks its directory in case of write operations.
        * 
        * @param directory Directory where IndexReader files reside.
        * @param segmentInfos Used for write-l
        * @param closeDirectory
        */
		IndexReader(CL_NS(store)::Directory* directory, SegmentInfos* segmentInfos, bool closeDirectory);
		

		/// Implements close. 
		virtual void doClose() = 0;

        /** Implements setNorm in subclass.*/
        virtual void doSetNorm(int32_t doc, const TCHAR* field, uint8_t value) = 0;
          
        /** Implements actual undeleteAll() in subclass. */
        virtual void doUndeleteAll() = 0;


        /** Implements deletion of the document numbered <code>docNum</code>.
        * Applications should call {@link #delete(int32_t)} or {@link #delete(Term)}.
        */
	    virtual void doDelete(const int32_t docNum) = 0;

	public:
      
      DEFINE_MUTEX(THIS_LOCK)

      ///Do not access this directly, only public so that MultiReader can access it
      virtual void commit();


  		/** Undeletes all documents currently marked as deleted in this index.*/
      void undeleteAll();

      /**
      * Returns a list of all unique field names that exist in the index pointed
      * to by this IndexReader.
      * @memory All memory must be cleaned by caller
      * @return Collection of Strings indicating the names of the fields
      * @throws IOException if there is a problem with accessing the index
      */
      virtual TCHAR** getFieldNames() = 0;

      /**
      * Returns a list of all unique field names that exist in the index pointed
      * to by this IndexReader.  The boolean argument specifies whether the fields
      * returned are indexed or not.
      * @memory All memory must be cleaned by caller
      * @param indexed <code>true</code> if only indexed fields should be returned;
      *                <code>false</code> if only unindexed fields should be returned.
      * @return Collection of Strings indicating the names of the fields
      * @throws IOException if there is a problem with accessing the index
      */
      virtual TCHAR** getFieldNames(bool indexed) = 0;

      /**
      * 
      * @memory All memory must be cleaned by caller
      * @param storedTermVector if true, returns only Indexed fields that have term vector info, 
      *                        else only indexed fields without term vector info 
      * @return Collection of Strings indicating the names of the fields
      */ 
      virtual TCHAR** getIndexedFieldNames(bool storedTermVector) = 0;


      /** Returns the byte-encoded normalization factor for the named field of
      * every document.  This is used by the search code to score documents.
      *
	  * The number of bytes returned is the size of the IndexReader->maxDoc()
	  * MEMORY: The values are cached, so don't delete the returned byte array.
      * @see Field#setBoost(float_t)
      */
		virtual uint8_t* norms(const TCHAR* field) = 0;


      /** Reads the byte-encoded normalization factor for the named field of every
      *  document.  This is used by the search code to score documents.
      *
      * @see Field#setBoost(float_t)
      */
      virtual void norms(const TCHAR* field, uint8_t* bytes, const int32_t offset) = 0;



        /** Expert: Resets the normalization factor for the named field of the named
        * document.
        *
        * @see #norms(String)
        * @see Similarity#decodeNorm(byte)
        */
        void setNorm(int32_t doc, const TCHAR* field, float_t value);
      
	    /** Expert: Resets the normalization factor for the named field of the named
	    * document.  The norm represents the product of the field's {@link
	    * Field#setBoost(float_t) boost} and its {@link Similarity#lengthNorm(String,
	    * int32_t) length normalization}.  Thus, to preserve the length normalization
	    * values when resetting this, one should base the new value upon the old.
	    *
	    * @see #norms(String)
	    * @see Similarity#decodeNorm(byte)
	    */
        void setNorm(int32_t doc, const TCHAR* field, uint8_t value);

		/// Release the write lock, if needed. 
	    virtual ~IndexReader();

		/// Returns an IndexReader reading the index in an FSDirectory in the named path. 
		static IndexReader* open(const char* path);

		/// Returns an IndexReader reading the index in the given Directory. 
		static IndexReader* open( CL_NS(store)::Directory* directory, bool closeDirectory=false);

		/** 
        * Returns the time the index in the named directory was last modified. 
        * 
        * <p>Synchronization of IndexReader and IndexWriter instances is 
        * no longer done via time stamps of the segments file since the time resolution 
        * depends on the hardware platform. Instead, a version number is maintained
        * within the segments file, which is incremented everytime when the index is
        * changed.</p>
        * 
        * @deprecated  Replaced by {@link #getCurrentVersion(String)}
        */
		static uint64_t lastModified(const char* directory);

		/** 
      * Returns the time the index in the named directory was last modified. 
      * 
      * <p>Synchronization of IndexReader and IndexWriter instances is 
      * no longer done via time stamps of the segments file since the time resolution 
      * depends on the hardware platform. Instead, a version number is maintained
      * within the segments file, which is incremented everytime when the index is
      * changed.</p>
      * 
      * @deprecated  Replaced by {@link #getCurrentVersion(Directory)}
      * */
		static uint64_t lastModified(const CL_NS(store)::Directory* directory);


  /**
   * Reads version number from segments files. The version number counts the
   * number of changes of the index.
   * 
   * @param directory where the index resides.
   * @return version number.
   * @throws IOException if segments file cannot be read.
   */
      static int64_t getCurrentVersion(CL_NS(store)::Directory* directory);
      	
	/**
	* Reads version number from segments files. The version number counts the
	* number of changes of the index.
	* 
	* @param directory where the index resides.
	* @return version number.
	* @throws IOException if segments file cannot be read
	*/
      static int64_t getCurrentVersion(const char* directory);

      
      /** Return an array of term frequency vectors for the specified document.
      *  The array contains a vector for each vectorized field in the document.
      *  Each vector contains terms and frequencies for all terms
      *  in a given vectorized field.
      *  If no such fields existed, the method returns null.
      *
      * @see Field#isTermVectorStored()
      */
      virtual TermFreqVector** getTermFreqVectors(int32_t docNumber) =0;

      /** Return a term frequency vector for the specified document and field. The
      *  vector returned contains terms and frequencies for those terms in
      *  the specified field of this document, if the field had storeTermVector
      *  flag set.  If the flag was not set, the method returns null.
      *
      * @see Field#isTermVectorStored()
      */
      virtual TermFreqVector* getTermFreqVector(int32_t docNumber, const TCHAR* field) = 0;
		
		///Checks if an index exists in the named directory
		static bool indexExists(const char* directory);

        //Checks if an index exists in the directory
		static bool indexExists(const CL_NS(store)::Directory* directory);

		///Returns the number of documents in this index. 
		virtual int32_t numDocs() = 0;

		///Returns one greater than the largest possible document number.
		///This may be used to, e.g., determine how big to allocate an array which
		///will have an element for every document number in an index.
		virtual int32_t maxDoc() const = 0;

		///Returns the stored fields of the n-th Document in this index. 
		virtual CL_NS(document)::Document* document(const int32_t n) =0;

		///Returns true if document n has been deleted 
		virtual bool isDeleted(const int32_t n) = 0;

		/** Returns true if any documents have been deleted */
		virtual bool hasDeletions() = 0;

		///Returns an enumeration of all the terms in the index.
		///The enumeration is ordered by Term.compareTo().  Each term
		///is greater than all that precede it in the enumeration.
		virtual TermEnum* terms() const =0;

		///Returns an enumeration of all terms after a given term.
		///The enumeration is ordered by Term.compareTo().  Each term
		///is greater than all that precede it in the enumeration.
		virtual TermEnum* terms(const Term* t) const = 0;

		///Returns the number of documents containing the term t. 
		virtual int32_t docFreq(const Term* t) const = 0;

		/// Returns an unpositioned TermPositions enumerator. 
		virtual TermPositions* termPositions() const = 0;
		
        //Returns an enumeration of all the documents which contain  term. For each 
        //document, in addition to the document number and frequency of the term in 
        //that document, a list of all of the ordinal positions of the term in the document 
        //is available.		
		TermPositions* termPositions(Term* term) const;

		/// Returns an unpositioned TermDocs enumerator. 
		virtual TermDocs* termDocs() const = 0;

		///Returns an enumeration of all the documents which contain term. 
		TermDocs* termDocs(Term* term) const;

		///Deletes the document numbered docNum.  Once a document is deleted it will not appear 
        ///in TermDocs or TermPostitions enumerations. Attempts to read its field with the document 
        ///method will result in an error.  The presence of this document may still be reflected in 
        ///the docFreq statistic, though this will be corrected eventually as the index is further modified.  
        ///Note: API renamed, because delete is a reserved word in c++.
		void deleteDocument(const int32_t docNum);

		///@Deprecated. Use deleteDocument instead.
		void deleteDoc(const int32_t docNum){ deleteDocument(docNum); }

		///Deletes all documents containing term. Returns the number of deleted documents
		int32_t deleteDocuments(Term* term);

		///@Deprecated. Use deleteDocuments instead.
		int32_t deleteTerm(Term* term){ return deleteDocuments(term); }

		/** 
		* Closes files associated with this index and also saves any new deletions to disk.
        * No other methods should be called after this has been called.
        */
		void close();

      ///Checks if the index in the named directory is currently locked.       
      static bool isLocked(CL_NS(store)::Directory* directory);

      ///Checks if the index in the named directory is currently locked.       
		static bool isLocked(const char* directory);


		///Forcibly unlocks the index in the named directory.
		///Caution: this should only be used by failure recovery code,
		///when it is known that no other process nor thread is in fact
		///currently accessing this index.
		static void unlock(CL_NS(store)::Directory* directory);
		static void unlock(const char* path);

		 /** Returns the directory this index resides in. */
		CL_NS(store)::Directory* getDirectory() { return directory; }



#ifndef LUCENE_HIDE_INTERNAL
		//this should be protected, but MSVC 6 does not allow access
		//to these fuctions in the protected classes IndexReaderLockWith
		//which is wrong, since they themselves are members of the class!!

		///for internal use. Public so that lock class can access it
		SegmentInfos* segmentInfos;
      
        /** Internal use. Implements commit. Public so that lock class can access it*/
        virtual void doCommit() = 0;
#endif

		/**
		* For classes that need to know when the IndexReader closes (such as caches, etc),
		* should pass their callback function to this.
		*/
		void addCloseCallback(CloseCallback callback, void* parameter);

	  protected:
		class IndexReaderLockWith:public CL_NS(store)::LuceneLockWith{
		public:
			CL_NS(store)::Directory* directory;
			IndexReader* indexReader;

			//Constructor	
			IndexReaderLockWith(CL_NS(store)::LuceneLock* lock, CL_NS(store)::Directory* dir);

			//Reads the segmentinfo file and depending on the number of segments found
			//it returns a MultiReader or a SegmentReader
			void* doBody();

		};

	    class IndexReaderCommitLockWith:public CL_NS(store)::LuceneLockWith{
	    private:
		    IndexReader* reader;
		public:
    			
		    //Constructor	
		    IndexReaderCommitLockWith( CL_NS(store)::LuceneLock* lock, IndexReader* r );
		    void* doBody();
		};
	};
	
CL_NS_END
#endif


