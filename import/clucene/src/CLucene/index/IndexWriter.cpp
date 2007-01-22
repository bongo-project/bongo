/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#include "CLucene/StdHeader.h"
#include "IndexWriter.h"

#include "CLucene/document/Document.h"
#include "CLucene/store/Directory.h"
#include "CLucene/store/Lock.h"
#include "CLucene/store/TransactionalRAMDirectory.h"
#include "CLucene/util/VoidList.h"
#include "DocumentWriter.h"
#include "SegmentInfos.h"
#include "SegmentMerger.h"

CL_NS_USE(store)
CL_NS_USE(util)
CL_NS_USE(document)
CL_NS_USE(analysis)
CL_NS_DEF(index)

  IndexWriter::IndexWriter(const char* path, Analyzer* a, const bool create, const bool _closeDir):
		directory( FSDirectory::getDirectory(path, create) ),
		analyzer(a),
		segmentInfos (_CLNEW SegmentInfos),
    closeDir(_closeDir){
  //Func - Constructor
  //       Constructs an IndexWriter for the index in path.
  //Pre  - path != NULL and contains a named directory path
  //       a holds a valid reference to an analyzer and analyzes the text to be indexed
  //       create indicates if the indexWriter must create a new index located at path or just open it
  //Post - If create is true, then a new, empty index has been created in path, replacing the index
  //       already there, if any. The named directory path is owned by this Instance

	  CND_PRECONDITION(path != NULL, "path is NULL");

	  //Continue initializing the instance by _IndexWriter
	  _IndexWriter ( create );
  }

  IndexWriter::IndexWriter(Directory* d, Analyzer* a, const bool create, const bool _closeDir):
	  directory(_CL_POINTER(d)),
	  analyzer(a),
	  segmentInfos (_CLNEW SegmentInfos),
      closeDir(_closeDir)
  {
  //Func - Constructor
  //       Constructs an IndexWriter for the index in path.
  //Pre  - d contains a valid reference to a directory
  //       a holds a valid reference to an analyzer and analyzes the text to be indexed
  //       create indicates if the indexWriter must create a new index located at path or just open it
  //Post - If create is true, then a new, empty index has been created in path, replacing the index
  //       already there, if any. The directory d is not owned by this Instance

	  //Continue initializing the instance by _IndexWriter
	  _IndexWriter ( create );
  }

  void IndexWriter::_IndexWriter(const bool create){
  //Func - Initialises the instances
  //Pre  - create indicates if the indexWriter must create a new index located at path or just open it
  //Post -
	  maxFieldLength = IndexWriter::DEFAULT_MAX_FIELD_LENGTH;

   similarity = CL_NS(search)::Similarity::getDefault();

   useCompoundFile = true;

	//Create a ramDirectory
	ramDirectory = _CLNEW TransactionalRAMDirectory;

	CND_CONDITION(ramDirectory != NULL,"ramDirectory is NULL");

	//Initialize the writeLock to
	writeLock  = NULL;
	//Initialize the mergeFactor to 10 indicating that a merge will occur after 10 documents
	//have been added to the index managed by this IndexWriter
	mergeFactor = 10;
	//Initialize maxMergeDocs to INT_MAX
	maxMergeDocs = INT_MAX;

   //initialise to LUCENE_INDEXWRITER_DEFAULT_MIN_MERGE_DOCS
   minMergeDocs = LUCENE_INDEXWRITER_DEFAULT_MIN_MERGE_DOCS;

	//Create a new lock using the name "write.lock"
	LuceneLock* newLock = directory->makeLock("write.lock");

	//Condition check to see if newLock has been allocated properly
	CND_CONDITION(newLock != NULL, "No memory could be allocated for LuceneLock newLock");

	//Try to obtain a write lock
	if (!newLock->obtain(LUCENE_WRITE_LOCK_TIMEOUT)){
		//Write lock could not be obtained so delete it
		_CLDELETE(newLock);
		//Reset the instance
		_finalize();
		//throw an exception because no writelock could be created or obtained
		_CLTHROWA(CL_ERR_IO, "Index locked for write or no write access." );
	}

	//The Write Lock has been obtained so save it for later use
	writeLock = newLock;

	//Create a new lock using the name "commit.lock"
	LuceneLock* lock = directory->makeLock("commit.lock");

	//Condition check to see if lock has been allocated properly
	CND_CONDITION(lock != NULL, "No memory could be allocated for LuceneLock lock");

	IndexWriterLockWith with ( lock,LUCENE_WRITE_LOCK_TIMEOUT,this,create );

	{
		SCOPED_LOCK_MUTEX(directory->THIS_LOCK) // in- & inter-process sync
		with.run();
	}

	//Release the commit lock
	_CLDELETE(lock);

   isOpen = true;
  }

  void IndexWriter::_finalize(){
  //Func - Releases all the resources of the instance
  //Pre  - true
  //Post - All the releases have been released

	  if(writeLock != NULL){
		  //release write lock
		  writeLock->release();
		  _CLDELETE( writeLock );
	  }

	  //Delete the ramDirectory
	  if ( ramDirectory != NULL ){
			ramDirectory->close();
			_CLDECDELETE(ramDirectory);
	  }

	  //clear segmentInfos and delete it
	  _CLDELETE(segmentInfos);

  }

  IndexWriter::~IndexWriter() {
  //Func - Destructor
  //Pre  - true
  //Post - The instance has been destroyed
     close();
	 _finalize();
  }


  void* IndexWriterLockWith::doBody() {
  //Func - Writes segmentInfos to or reads  segmentInfos from disk
  //Pre  - writer != NULL
  //Post - if create is true then segementInfos has been written to disk otherwise
  //       segmentInfos has been read from disk

	  CND_PRECONDITION(writer != NULL, "writer is NULL");

	  if (create)
		  writer->segmentInfos->write(writer->getDirectory());
	  else
		  writer->segmentInfos->read(writer->getDirectory());

	  return NULL;
  }

  void* IndexWriterLockWith2::doBody(){
  //Func - Writes the segmentInfos to Disk and deletes unused segments
  //Pre  - writer != NULL
  //Post - segmentInfos have been written to disk and unused segments have been deleted

	  CND_PRECONDITION(writer != NULL, "writer is NULL");

	  //commit before deleting
	  writer->segmentInfos->write(writer->getDirectory());
	  //delete now-unused segments
	  writer->deleteSegments(segmentsToDelete);

	  return NULL;
  }

  void IndexWriter::close( ) {
  //Func - Flushes all changes to an index, closes all associated files, and closes
  //       the directory that the index is stored in.
  //Pre  - closeDir indicates if the directory must be closed or not
  //Post - All the changes have been flushed to disk and the write lock has been released
  //       The ramDirectory has also been closed. The directory has been closed
  //       if the reference count of the directory reaches zero

	 SCOPED_LOCK_MUTEX(THIS_LOCK)
     if ( isOpen ){
	   //Flush the Ram Segments
	   flushRamSegments();
	   //Close the ram directory
	   if ( ramDirectory != NULL ){
		  ramDirectory->close();
		  _CLDECDELETE(ramDirectory);
	   }

	   //Check if this instance must close the directory
	   if ( closeDir ){
		   directory->close();
	   }
	   _CLDECDELETE(directory);

      // release write lock
	   if (writeLock != NULL){
		   writeLock->release();
		   _CLDELETE( writeLock );
	   }

       isOpen = false;
     }
  }


  int32_t IndexWriter::docCount(){
  //Func - Counts the number of documents in the index
  //Pre  - true
  //Post - The number of documents have been returned

	  SCOPED_LOCK_MUTEX(THIS_LOCK)
	  
	  //Initialize count
	  int32_t count = 0;

	  //Iterate through all segmentInfos
	  for (int32_t i = 0; i < segmentInfos->size(); i++) {
		  //Get the i-th SegmentInfo
		  SegmentInfo* si = segmentInfos->info(i);
		  //Retrieve the number of documents of the segment and add it to count
		  count += si->docCount;
      }
	  return count;
  }

  void IndexWriter::addDocument(Document* doc) {
  //Func - Adds a document to the index
  //Pre  - doc contains a valid reference to a document
  //       ramDirectory != NULL
  //Post - The document has been added to the index of this IndexWriter
	CND_PRECONDITION(ramDirectory != NULL,"ramDirectory is NULL");

	ramDirectory->transStart();
	try {
		char* segmentName = newSegmentName();
		CND_CONDITION(segmentName != NULL, "segmentName is NULL");
		try {
			//Create the DocumentWriter using a ramDirectory and analyzer
			// supplied by the IndexWriter (this).
			DocumentWriter* dw = _CLNEW DocumentWriter(
				ramDirectory, analyzer, similarity, maxFieldLength );
			CND_CONDITION(dw != NULL, "dw is NULL");
			try {
				//Add the client-supplied document to the new segment.
				dw->addDocument(segmentName, doc);
			} _CLFINALLY(
				_CLDELETE(dw);
			);

			//Create a new SegmentInfo instance about this new segment.
			SegmentInfo* si = _CLNEW SegmentInfo(segmentName, 1, ramDirectory);
			CND_CONDITION(si != NULL, "Si is NULL");

			{
				SCOPED_LOCK_MUTEX(THIS_LOCK)

   				//Add the info object for this particular segment to the list
   				// of all segmentInfos->
   				segmentInfos->add(si);
	   			
          		//Check to see if the segments must be merged
          		maybeMergeSegments();
			}
		} _CLFINALLY(
			_CLDELETE_CaARRAY(segmentName);
		);
		
	} catch (...) {
		ramDirectory->transAbort();
		throw;
	}
	ramDirectory->transCommit();
  }


  void IndexWriter::optimize() {
  //Func - Optimizes the index for which this Instance is responsible
  //Pre  - true
  //Post -
    SCOPED_LOCK_MUTEX(THIS_LOCK)
	//Flush the RamSegments to disk
    flushRamSegments();
    while (segmentInfos->size() > 1 ||
		(segmentInfos->size() == 1 &&
			(SegmentReader::hasDeletions(segmentInfos->info(0)) ||
            segmentInfos->info(0)->getDir()!=directory ||
            (useCompoundFile &&
              (!SegmentReader::usesCompoundFile(segmentInfos->info(0)) ||
                SegmentReader::hasSeparateNorms(segmentInfos->info(0))))))) {

		int32_t minSegment = segmentInfos->size() - mergeFactor;

		mergeSegments(minSegment < 0 ? 0 : minSegment);
	}
  }


  char* IndexWriter::newSegmentName() {
    SCOPED_LOCK_MUTEX(THIS_LOCK)

    TCHAR buf[9];
    _i64tot(segmentInfos->counter++,buf,36); //36 is RADIX of 10 digits and 26 numbers

	int32_t rlen = _tcslen(buf) + 2;
	char* ret = _CL_NEWARRAY(char,rlen);
	strcpy(ret,"_");
	STRCPY_TtoA(ret+1,buf,rlen-1); //write at 2nd character, for a maximum of 9 characters
    return ret;
  }

  void IndexWriter::flushRamSegments() {
  //Func - Merges all RAM-resident segments.
  //Pre  - ramDirectory != NULL
  //Post - The RAM-resident segments have been merged to disk

	CND_PRECONDITION(ramDirectory != NULL, "ramDirectory is NULL");

    int32_t minSegment = segmentInfos->size()-1; //don't make this unsigned...
	CND_CONDITION(minSegment >= -1, "minSegment must be >= -1");

	int32_t docCount = 0;
	//Iterate through all the segements and check if the directory is a ramDirectory
	while (minSegment >= 0 &&
	  segmentInfos->info(minSegment)->getDir() == ramDirectory) {
	  docCount += segmentInfos->info(minSegment)->docCount;
	  minSegment--;
	}
	if (minSegment < 0 ||			  // add one FS segment?
		(docCount + segmentInfos->info(minSegment)->docCount) > mergeFactor ||
		!(segmentInfos->info(segmentInfos->size()-1)->getDir() == ramDirectory))
	  minSegment++;

	CND_CONDITION(minSegment >= 0, "minSegment must be >= 0");
	if (minSegment >= segmentInfos->size())
	  return;					  // none to merge
	mergeSegments(minSegment);
  }

  void IndexWriter::maybeMergeSegments() {
  //Func - Incremental Segment Merger
  //Pre  -
  //Post -

	int64_t targetMergeDocs = minMergeDocs;

	// find segments smaller than current target size
	while (targetMergeDocs <= maxMergeDocs) {
		int32_t minSegment = segmentInfos->size();
		int32_t mergeDocs = 0;

		while (--minSegment >= 0) {
			SegmentInfo* si = segmentInfos->info(minSegment);
			if (si->docCount >= targetMergeDocs)
				break;
			mergeDocs += si->docCount;
		}

		if (mergeDocs >= targetMergeDocs){
			// found a merge to do
			mergeSegments(minSegment+1);
		}else
			break;

		//increase target size
		targetMergeDocs *= mergeFactor;
	}
  }


  void IndexWriter::mergeSegments(const uint32_t minSegment) {
    CLVector<SegmentReader*> segmentsToDelete(false);
    const char* mergedName = newSegmentName();
#ifdef _CL_DEBUG_INFO
	fprintf(_CL_DEBUG_INFO, "merging segments\n");
#endif
    SegmentMerger merger(directory, mergedName, useCompoundFile);
    for (int32_t i = minSegment; i < segmentInfos->size(); i++) {
      SegmentInfo* si = segmentInfos->info(i);
#ifdef _CL_DEBUG_INFO
	  fprintf(_CL_DEBUG_INFO, " %s (%d docs)\n",si->name,si->docCount);
#endif
      SegmentReader* reader = _CLNEW SegmentReader(si);
      merger.add(reader);
      if ((reader->getDirectory() == this->directory) || // if we own the directory
		(reader->getDirectory() == this->ramDirectory)){
        segmentsToDelete.push_back((SegmentReader*)reader);	  // queue segment for deletion
	  }
    }

    int32_t mergedDocCount = merger.merge();

#ifdef _CL_DEBUG_INFO
	 fprintf(_CL_DEBUG_INFO,"\n into %s (%d docs)\n",mergedName, mergedDocCount);
#endif
	  
	segmentInfos->clearto(minSegment); // pop old infos & add new
    segmentInfos->add( _CLNEW SegmentInfo(mergedName, mergedDocCount, directory));


    // close readers before we attempt to delete now-obsolete segments
    merger.closeReaders();

    LuceneLock* lock = directory->makeLock("commit.lock");
    IndexWriterLockWith2 with ( lock,LUCENE_COMMIT_LOCK_TIMEOUT,this,&segmentsToDelete );

    {
    	SCOPED_LOCK_MUTEX(directory->THIS_LOCK) // in- & inter-process sync
    	with.run();
    }

    _CLDELETE( lock );
    _CLDELETE_CaARRAY( mergedName ); //ADD:
  }

  void IndexWriter::deleteSegments(CLVector<SegmentReader*>* segments) {
    AStringArrayConstWithDeletor deletable;

    AStringArrayConstWithDeletor* deleteArray = readDeleteableFiles();
    deleteFiles(deleteArray, &deletable); // try to delete deleteable
    _CLDELETE(deleteArray);

    for (uint32_t i = 0; i < segments->size(); i++) {
      SegmentReader* reader = (*segments)[i];
      AStringArrayConstWithDeletor* files = reader->files();
      if (reader->getDirectory() == this->directory)
        deleteFiles(files, &deletable);	  // try to delete our files
      else
        deleteFiles(files, reader->getDirectory()); // delete, eg, RAM files

      _CLDELETE(files);
    }

    writeDeleteableFiles(&deletable);		  // note files we can't delete
  }

  AStringArrayConstWithDeletor* IndexWriter::readDeleteableFiles() {
    AStringArrayConstWithDeletor* result = _CLNEW AStringArrayConstWithDeletor;

    if (!directory->fileExists("deletable"))
      return result;

    IndexInput* input = directory->openInput("deletable");
    try {
		TCHAR tname[CL_MAX_PATH];
		for (int32_t i = input->readInt(); i > 0; i--){	  // read file names
			input->readString(tname,CL_MAX_PATH);
			result->push_back(STRDUP_TtoA(tname));
		}
    } _CLFINALLY(
        input->close();
        _CLDELETE(input);
    );


    return result;
  }

  void IndexWriter::writeDeleteableFiles(AStringArrayConstWithDeletor* files) {
    IndexOutput* output = directory->createOutput("deleteable.new");
    try {
      output->writeInt(files->size());
	  TCHAR tfile[CL_MAX_PATH]; //temporary space for tchar file name
	  for (uint32_t i = 0; i < files->size(); i++){
		STRCPY_AtoT(tfile,(*files)[i],CL_MAX_PATH);
        output->writeString( tfile, _tcslen(tfile) );
	  }
    } _CLFINALLY(
        output->close();
        _CLDELETE(output);
    );

    directory->renameFile("deleteable.new", "deletable");
  }

  void IndexWriter::deleteFiles(AStringArrayConstWithDeletor* files, Directory* directory) {
	AStringArrayConstWithDeletor::const_iterator itr = files->begin();
	while ( itr != files->end() ){
		directory->deleteFile( *itr );
		itr++;
	}
  }

  void IndexWriter::deleteFiles(AStringArrayConstWithDeletor* files, AStringArrayConstWithDeletor* deletable) {
	  AStringArrayConstWithDeletor::const_iterator itr=files->begin();
	  while ( itr != files->end() ){
		const char* file = *itr;
		try {
			if ( directory->fileExists(file) )
				directory->deleteFile(file);		  // try to delete each file
		} catch (CLuceneError& err) {			  // if delete fails
		    if ( err.number() != CL_ERR_IO )
		        throw err; //not an IO err... re-throw

			if (directory->fileExists(file)) {
	#ifdef _CL_DEBUG_INFO
				fprintf(_CL_DEBUG_INFO,"%s; Will re-try later.\n", err.what());
	#endif
			deletable->push_back(STRDUP_AtoA(file));		  // add to deletable
			}
		}
	  itr++;
	 }
  }


  
  void IndexWriter::addIndexes(Directory** dirs) {
  //Func - Add several indexes located in different directories into the current
  //       one managed by this instance
  //Pre  - dirs != NULL and contains directories of several indexes
  //       dirsLength > 0 and contains the number of directories
  //Post - The indexes located in the directories in dirs have been merged with
  //       the pre(current) index. The Resulting index has also been optimized

	  SCOPED_LOCK_MUTEX(THIS_LOCK)
	  
	  CND_PRECONDITION(dirs != NULL, "dirs is NULL");

	  // start with zero or 1 seg so optimize the current
	  optimize();

	  //Iterate through the directories
     int32_t i = 0;
	  while ( dirs[i] != NULL ) {
		  // DSR: Changed SegmentInfos constructor arg (see bug discussion below).
		  SegmentInfos sis(false);
		  sis.read( dirs[i]);

		  for (int32_t j = 0; j < sis.size(); j++) {
		   /* DSR:CL_BUG:
		   ** In CLucene 0.8.11, the next call placed a pointer to a SegmentInfo
		   ** object from stack variable $sis into the vector this->segmentInfos.
		   ** Then, when the call to optimize() is made just before exiting this
		   ** function, $sis had already been deallocated (and has deleted its
		   ** member objects), leaving dangling pointers in this->segmentInfos.
		   ** I added a SegmentInfos constructor that allowed me to order it not
		   ** to delete its members, invoked the new constructor form above for
		   ** $sis, and the problem was solved. */
		   segmentInfos->add(sis.info(j));	  // add each info
		  }
        i++;
	}
	optimize();					  // cleanup
  }


  void IndexWriter::addIndexes(IndexReader** readers){
	 SCOPED_LOCK_MUTEX(THIS_LOCK)
    optimize();					  // start with zero or 1 seg

    char* mergedName = newSegmentName();
    SegmentMerger* merger = _CLNEW SegmentMerger(directory, mergedName, false);

    if (segmentInfos->size() == 1)                 // add existing index, if any
      merger->add(_CLNEW SegmentReader(segmentInfos->info(0)));

    int32_t readersLength = 0;
    while ( readers[readersLength] != NULL )
      merger->add((SegmentReader*) readers[readersLength++]);

    int32_t docCount = merger->merge();                // merge 'em

    // pop old infos & add new
	segmentInfos->clearto(0);
    segmentInfos->add(_CLNEW SegmentInfo(mergedName, docCount, directory));

    LuceneLock* lock = directory->makeLock("commit.lock");
    IndexWriterLockWith with ( lock,LUCENE_COMMIT_LOCK_TIMEOUT,this,true);

	{
		SCOPED_LOCK_MUTEX(directory->THIS_LOCK) // in- & inter-process sync
	   	with.run();
	}

      _CLDELETE(lock);
   }

CL_NS_END
