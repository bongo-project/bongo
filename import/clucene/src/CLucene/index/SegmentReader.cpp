/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#include "CLucene/StdHeader.h"
#include "SegmentHeader.h"

#include "FieldInfos.h"
#include "FieldsReader.h"
#include "IndexReader.h"
#include "TermInfosReader.h"
#include "CLucene/util/BitVector.h"
#include "Terms.h"

CL_NS_USE(util)
CL_NS_USE(store)
CL_NS_USE(document)
CL_NS_DEF(index)

 SegmentReader::Norm::Norm(IndexInput* instrm, int32_t n, SegmentReader* r, const char* seg): 
   in(instrm), number(n), reader(r), segment(seg){
  //Func - Constructor
  //Pre  - instrm is a valid reference to an IndexInput
  //Post - A Norm instance has been created with an empty bytes array

	  bytes = NULL;
     dirty = false;
  }

  SegmentReader::Norm::~Norm() {
  //Func - Destructor
  //Pre  - true
  //Post - The IndexInput in has been deleted (and closed by its destructor) 
  //       and the array too.

      //Close and destroy the inputstream in-> The inputstream will be closed
      // by its destructor. Note that the IndexInput 'in' actually is a pointer!!!!!  
      _CLDELETE(in);

	  //Delete the bytes array
      _CLDELETE_ARRAY(bytes);

  }

  void SegmentReader::Norm::reWrite(){
      char buf[CL_MAX_PATH];
      char fileName[CL_MAX_PATH];
      sprintf(buf,"%s.tmp",segment);

      // NOTE: norms are re-written in regular directory, not cfs
      IndexOutput* out = reader->getDirectory()->createOutput(buf);
      try {
        out->writeBytes(bytes, reader->maxDoc());
      }_CLFINALLY( out->close(); _CLDELETE(out) );

      sprintf(fileName,"%s.f%d",segment,number);
      reader->getDirectory()->renameFile(buf, fileName);
      this->dirty = false;
    }

  SegmentReader::SegmentReader(SegmentInfo* si) : 
      //Init the superclass IndexReader
      IndexReader(si->getDir()),
	  _norms(false,false)
  { 
      initialize(si);
  }

  SegmentReader::SegmentReader(SegmentInfos* sis, SegmentInfo* si) : 
      //Init the superclass IndexReader
      IndexReader(si->getDir(),sis,false),
	  _norms(false,false)
  { 
      initialize(si);
  }

  void SegmentReader::initialize(SegmentInfo* si){
      //Pre  - si-> is a valid reference to SegmentInfo instance
      //       identified by si->
      //Post - All files of the segment have been read

      deletedDocs      = NULL;
	  //There are no documents yet marked as deleted
      deletedDocsDirty = false;
      
      normsDirty=false;
      undeleteAll=false;

	  //Duplicate the name of the segment from SegmentInfo to segment
      segment          = STRDUP_AtoA(si->name);
	  // make sure that all index files have been read or are kept open
      // so that if an index update removes them we'll still have them
      freqStream       = NULL;
      proxStream       = NULL;
      
	  //instantiate a buffer large enough to hold a directory path
      char buf[CL_MAX_PATH];

      // Use compound file directory for some files, if it exists
      Directory* cfsDir = getDirectory();
      SegmentName(buf, CL_MAX_PATH, ".cfs");
      if (cfsDir->fileExists(buf)) {
         cfsReader = _CLNEW CompoundFileReader(cfsDir, buf);
         cfsDir = cfsReader;
	  }else
		 cfsReader = NULL;

	  //Create the name of the field info file with suffix .fnm in buf
      SegmentName(buf, CL_MAX_PATH, ".fnm");
      fieldInfos = _CLNEW FieldInfos(cfsDir, buf );

      //Condition check to see if fieldInfos points to a valid instance
      CND_CONDITION(fieldInfos != NULL,"No memory could be allocated for fieldInfos");

	  //Create the name of the frequence file with suffix .frq in buf
      SegmentName(buf,CL_MAX_PATH, ".frq");

	  //Open an IndexInput freqStream to the frequency file
#ifdef LUCENE_FS_MMAP
	  if ( strcmp(cfsDir->getDirectoryType(), "FS") == 0 ){
		  FSDirectory* fsdir = (FSDirectory*)cfsDir;
		  freqStream = fsdir->openMMapFile( buf );
	  }else
#endif
		freqStream = cfsDir->openInput( buf );

      //Condition check to see if freqStream points to a valid instance and was able to open the
	  //frequency file
      CND_CONDITION(freqStream != NULL, "IndexInput freqStream could not open the frequency file");

	  //Create the name of the prox file with suffix .prx in buf
      SegmentName(buf, CL_MAX_PATH,".prx");

	  //Open an IndexInput proxStream to the prox file
      proxStream = cfsDir->openInput( buf );

	  //Condition check to see if proxStream points to a valid instance and was able to open the
	  //prox file
      CND_CONDITION(proxStream != NULL, "IndexInput proxStream could not open proximity file");

	  //Instantiate a FieldsReader for reading the Field Info File
      fieldsReader = _CLNEW FieldsReader(cfsDir, segment, fieldInfos);

      //Condition check to see if fieldsReader points to a valid instance 
      CND_CONDITION(fieldsReader != NULL,"No memory could be allocated for fieldsReader");

	  //Instantiate a TermInfosReader for reading the Term Dictionary .tis file
      tis = _CLNEW TermInfosReader(cfsDir, segment, fieldInfos);

      //Condition check to see if tis points to a valid instance 
      CND_CONDITION(tis != NULL,"No memory could be allocated for tis");

	  //Check if the segment has deletion according to the SegmentInfo instance si->
      // NOTE: the bitvector is stored using the regular directory, not cfs
      if (hasDeletions(si)){
		  //Create a deletion file with suffix .del          
          SegmentName(buf, CL_MAX_PATH,".del");
		  //Instantiate a BitVector that manages which documents have been deleted
          deletedDocs = _CLNEW BitVector(getDirectory(), buf );
       }

	  //Open the norm file. There's a norm file for each indexed field with a byte for each document. 
	  //The .f[0-9]* file contains, for each document, a byte that encodes a value 
	  //that is multiplied into the score for hits on that field
      openNorms(cfsDir);

      if (fieldInfos->hasVectors()) { // open term vector files only as needed
         termVectorsReader = _CLNEW TermVectorsReader(cfsDir, segment, fieldInfos);
      }else
		  termVectorsReader = NULL;
  }

  SegmentReader::~SegmentReader(){
  //Func - Destructor.
  //Pre  - doClose has been invoked!
  //Post - the instance has been destroyed

      doClose(); //this means that index reader doesn't need to be closed manually

      _CLDELETE(fieldInfos);
	  _CLDELETE(fieldsReader);
      _CLDELETE(tis);	      
 	  _CLDELETE(freqStream);
	  _CLDELETE(proxStream);
	  _CLDELETE_CaARRAY(segment);
	  _CLDELETE(deletedDocs);
     _CLDELETE(termVectorsReader)
     _CLDECDELETE(cfsReader);
  }

  void SegmentReader::doCommit(){
   char bufdel[CL_MAX_PATH];
   strcpy(bufdel,segment);
   strcat(bufdel,".del");

    if (deletedDocsDirty) {               // re-write deleted 
       char buftmp[CL_MAX_PATH];
       strcpy(buftmp,segment);
       strcat(buftmp,".tmp");
      deletedDocs->write(getDirectory(), buftmp);
      getDirectory()->renameFile(buftmp,bufdel);
    }
    if(undeleteAll && getDirectory()->fileExists(bufdel)){
      getDirectory()->deleteFile(bufdel);
    }
    if (normsDirty) {               // re-write norms 
	  CL_NS(util)::CLHashtable<const TCHAR*,Norm*,Compare::TChar>::iterator itr = _norms.begin();
      while (itr != _norms.end()) {
        Norm* norm = itr->second;
        if (norm->dirty) {
          norm->reWrite();
        }
        itr++;
      }
    }
    deletedDocsDirty = false;
    normsDirty = false;
    undeleteAll = false;
  }
  
  void SegmentReader::doClose() {
  //Func - Closes all streams to the files of a single segment
  //Pre  - fieldsReader != NULL
  //       tis != NULL
  //Post - All streams to files have been closed

      CND_PRECONDITION(fieldsReader != NULL, "fieldsReader is NULL");
      CND_PRECONDITION(tis != NULL, "tis is NULL");

	  //Close the fieldsReader
      fieldsReader->close();
	  //Close the TermInfosReader
      tis->close();

	  //Close the frequency stream
	  if (freqStream != NULL){
          freqStream->close();
	  }
	  //Close the prox stream
	  if (proxStream != NULL){
         proxStream->close();
	   }

	  //Close the norm file
      closeNorms();
    
     if (termVectorsReader != NULL) 
        termVectorsReader->close();

     if (cfsReader != NULL)
         cfsReader->close();
  }

  bool SegmentReader::hasDeletions() {
      return deletedDocs != NULL;
  }

  //static 
  bool SegmentReader::usesCompoundFile(SegmentInfo* si) {
    char buf[CL_MAX_PATH];
    strcpy(buf,si->name);
    strcat(buf,".cfs");
    return si->getDir()->fileExists(buf);
  }
  
  //static
  bool SegmentReader::hasSeparateNorms(SegmentInfo* si) {
    char** result = si->getDir()->list();
    char pattern[CL_MAX_PATH];
    strcpy(pattern,si->name);
    strcat(pattern,".f");
    size_t patternLength = strlen(pattern);

    int32_t i=0;
    char* res=NULL;
	bool ret=false;
    while ( (res=result[i]) != NULL ){
		if ( !ret ){
			if ( strlen(res)>patternLength && strncmp(res,pattern,patternLength) == 0 ){
				if ( res[patternLength] >= '0' && res[patternLength] <= '9' )
					ret=true;
			}
		}
	  _CLDELETE_CaARRAY(result[i]);
	  i++;
    }
	_CLDELETE_ARRAY(result);
    return ret;
  }

  bool SegmentReader::hasDeletions(const SegmentInfo* si) {
  //Func - Static method
  //       Checks if a segment managed by SegmentInfo si-> has deletions
  //Pre  - si-> holds a valid reference to an SegmentInfo instance
  //Post - if the segement contains deleteions true is returned otherwise flas

	  //Create a buffer f of length CL_MAX_PATH
      char f[CL_MAX_PATH];
      //SegmentReader::segmentname(f, si->name,_T(".del"),-1 );
      //create the name of the deletion file
	  Misc::segmentname(f,CL_MAX_PATH, si->name,".del",-1 );
	  //Check if the deletion file exists and return the result
      return si->getDir()->fileExists( f );
  }

	//synchronized
  void SegmentReader::doDelete(const int32_t docNum){
  //Func - Marks document docNum as deleted
  //Pre  - docNum >=0 and DocNum < maxDoc() 
  //       docNum contains the number of the document that must be 
  //       marked deleted
  //Post - The document identified by docNum has been marked deleted

      SCOPED_LOCK_MUTEX(THIS_LOCK)
      
     CND_PRECONDITION(docNum >= 0, "docNum is a negative number");
     CND_PRECONDITION(docNum < maxDoc(), "docNum is bigger than the total number of documents");

	  //Check if deletedDocs exists
	  if (deletedDocs == NULL){
          deletedDocs = _CLNEW BitVector(maxDoc());

          //Condition check to see if deletedDocs points to a valid instance
          CND_CONDITION(deletedDocs != NULL,"No memory could be allocated for deletedDocs");
	  }
      //Flag that there are documents marked deleted
      deletedDocsDirty = true;
      undeleteAll = false;
      //Mark document identified by docNum as deleted
      deletedDocs->set(docNum);

  }

  void SegmentReader::doUndeleteAll(){
      _CLDELETE(deletedDocs);
      deletedDocsDirty = false;
      undeleteAll = true;
  }

  AStringArrayConstWithDeletor* SegmentReader::files() {
  //Func - Returns all file names managed by this SegmentReader
  //Pre  - segment != NULL
  //Post - All filenames managed by this SegmentRead have been returned
 
     CND_PRECONDITION(segment != NULL, "segment is NULL");

	  AStringArrayConstWithDeletor* files = _CLNEW AStringArrayConstWithDeletor(true);

     //Condition check to see if files points to a valid instance
     CND_CONDITION(files != NULL, "No memory could be allocated for files");

     char* temp = NULL;
     #define _ADD_SEGMENT(ext) temp = SegmentName( ext ); if ( getDirectory()->fileExists(temp) ) files->push_back(temp); else _CLDELETE_CaARRAY(temp);
								

     //Add the name of the Field Info file
     _ADD_SEGMENT(".cfs" );
     _ADD_SEGMENT(".fnm" );
     _ADD_SEGMENT(".fdx" );
     _ADD_SEGMENT(".fdt" );
     _ADD_SEGMENT(".tii" );
     _ADD_SEGMENT(".tis" );
     _ADD_SEGMENT(".frq" );
     _ADD_SEGMENT(".prx" );
     _ADD_SEGMENT(".del" );
     _ADD_SEGMENT(".tvx" );
     _ADD_SEGMENT(".tvd" );
     _ADD_SEGMENT(".tvf" );
     _ADD_SEGMENT(".tvp" );

      //iterate through the field infos
      for (int32_t i = 0; i < fieldInfos->size(); i++) {
          //Get the field info for the i-th field   
          FieldInfo* fi = fieldInfos->fieldInfo(i);
          //Check if the field has been indexed
          if (fi->isIndexed){
              //The field has been indexed so add its norm file
              files->push_back( SegmentName(".f", i) );
          }
       }

    return files;
  }

  TermEnum* SegmentReader::terms() const {
  //Func - Returns an enumeration of all the Terms and TermInfos in the set. 
  //Pre  - tis != NULL
  //Post - An enumeration of all the Terms and TermInfos in the set has been returned

      CND_PRECONDITION(tis != NULL, "tis is NULL");

      return tis->terms();
  }

  TermEnum* SegmentReader::terms(const Term* t) const {
  //Func - Returns an enumeration of terms starting at or after the named term t 
  //Pre  - t != NULL
  //       tis != NULL
  //Post - An enumeration of terms starting at or after the named term t 

      CND_PRECONDITION(t   != NULL, "t is NULL");
      CND_PRECONDITION(tis != NULL, "tis is NULL");

      return tis->terms(t);
  }

  Document* SegmentReader::document(const int32_t n) {
  //Func - Returns a document identified by n
  //Pre  - n >=0 and identifies the document n
  //Post - if the document has been deleted then an exception has been thrown
  //       otherwise a reference to the found document has been returned

      SCOPED_LOCK_MUTEX(THIS_LOCK)
      
      CND_PRECONDITION(n >= 0, "n is a negative number");

	  //Check if the n-th document has been marked deleted
       if (isDeleted(n)){
          _CLTHROWA( CL_ERR_InvalidState,"attempt to access a deleted document" );
          }

	   //Retrieve the n-th document
       Document* ret = fieldsReader->doc(n);

       //Condition check to see if ret points to a valid instance
       CND_CONDITION(ret != NULL, "No document could be retrieved");

	   //Return the document
       return ret;
  }


  bool SegmentReader::isDeleted(const int32_t n) {
  //Func - Checks if the n-th document has been marked deleted
  //Pre  - n >=0 and identifies the document n
  //Post - true has been returned if document n has been deleted otherwise fralse

      SCOPED_LOCK_MUTEX(THIS_LOCK)
      
      CND_PRECONDITION(n >= 0, "n is a negative number");

	  //Is document n deleted
      bool ret = (deletedDocs != NULL && deletedDocs->get(n));

      return ret;
  }

  TermDocs* SegmentReader::termDocs() const {
  //Func - Returns an unpositioned TermDocs enumerator. 
  //Pre  - true
  //Post - An unpositioned TermDocs enumerator has been returned

       return _CLNEW SegmentTermDocs((SegmentReader*)this);
  }

  TermPositions* SegmentReader::termPositions() const {
  //Func - Returns an unpositioned TermPositions enumerator. 
  //Pre  - true
  //Post - An unpositioned TermPositions enumerator has been returned

      return _CLNEW SegmentTermPositions((SegmentReader*)this);
  }

  int32_t SegmentReader::docFreq(const Term* t) const {
  //Func - Returns the number of documents which contain the term t
  //Pre  - t holds a valid reference to a Term
  //Post - The number of documents which contain term t has been returned

      //Get the TermInfo ti for Term  t in the set
      TermInfo* ti = tis->get(t);
      //Check if an TermInfo has been returned
      if (ti){
		  //Get the frequency of the term
          int32_t ret = ti->docFreq;
		  //TermInfo ti is not needed anymore so delete it
          _CLDELETE( ti );
		  //return the number of documents which containt term t
          return ret;
          }
	  else
		  //No TermInfo returned so return 0
          return 0;
  }

  int32_t SegmentReader::numDocs() {
  //Func - Returns the actual number of documents in the segment
  //Pre  - true
  //Post - The actual number of documents in the segments

	  //Get the number of all the documents in the segment including the ones that have 
	  //been marked deleted
      int32_t n = maxDoc();

	  //Check if there any deleted docs
      if (deletedDocs != NULL)
		  //Substract the number of deleted docs from the number returned by maxDoc
          n -= deletedDocs->count();

	  //return the actual number of documents in the segment
      return n;
  }

  int32_t SegmentReader::maxDoc() const {
  //Func - Returns the number of  all the documents in the segment including
  //       the ones that have been marked deleted
  //Pre  - true
  //Post - The total number of documents in the segment has been returned

      return fieldsReader->size();
  }


  void SegmentReader::norms(const TCHAR* field, uint8_t* bytes, const int32_t offset) {
  //Func - Reads the Norms for field from disk starting at offset in the inputstream
  //Pre  - field != NULL
  //       bytes != NULL is an array of bytes which is to be used to read the norms into.
  //       it is advisable to have bytes initalized by zeroes!
  //       offset >= 0
  //Post - The if an inputstream to the norm file could be retrieved the bytes have been read
  //       You are never sure whether or not the norms have been read into bytes properly!!!!!!!!!!!!!!!!!

    CND_PRECONDITION(field != NULL, "field is NULL");
    CND_PRECONDITION(bytes != NULL, "field is NULL");
    CND_PRECONDITION(offset >= 0, "offset is a negative number");

	 SCOPED_LOCK_MUTEX(THIS_LOCK)
    
    Norm* norm = _norms.get(field);
    if ( norm == NULL )
       return;					  // use zeros in array

   if (norm->bytes != NULL) { // can copy from cache
      //todo: can we use memcpy here... faster
      int32_t len = maxDoc();
      for ( int32_t i=0;i<len;i++ )
         bytes[i+offset]=norm->bytes[i];
      return;
    }

   IndexInput* _normStream = norm->in->clone();
   CND_PRECONDITION(_normStream != NULL, "normStream==NULL")

    // read from disk
    try{ 
       _normStream->seek(0);
       _normStream->readBytes(bytes, offset, maxDoc());
    }_CLFINALLY(
        //Have the normstream closed
        _normStream->close();
        //Destroy the normstream
        _CLDELETE( _normStream );
	);
	
  }

  uint8_t* SegmentReader::norms(const TCHAR* field) {
  //Func - Returns the bytes array that holds the norms of a named field
  //Pre  - field != NULL and contains the name of the field for which the norms 
  //       must be retrieved
  //Post - If there was norm for the named field then a bytes array has been allocated 
  //       and returned containing the norms for that field. If the named field is unknown NULL is returned.

    CND_PRECONDITION(field != NULL, "field is NULL");
    
    
	  SCOPED_LOCK_MUTEX(THIS_LOCK)

    //Try to retrieve the norms for field
    Norm* norm = (Norm*)_norms.get(field);
    //Check if a norm instance was found
    if (norm == NULL){
        //return NULL as there are no norms to be returned
        return NULL;
	}

    if (norm->bytes == NULL) { //value not read yet
        //allocate a new bytes array to hold the norms
        uint8_t* bytes = _CL_NEWARRAY(uint8_t,maxDoc()); 

        //Condition check to see if bytes points to a valid array
        CND_CONDITION(bytes != NULL, "bytes is NULL");

        //Read the norms from disk straight into the new bytes array
        norms(field, bytes, 0);
        norm->bytes = bytes; // cache it
    }

		//Return the norms
		return norm->bytes;
  }

  void SegmentReader::doSetNorm(int32_t doc, const TCHAR* field, uint8_t value){
    Norm* norm = _norms.get(field);
    if (norm == NULL)                             // not an indexed field
      return;
    norm->dirty = true;                            // mark it dirty
    normsDirty = true;

    uint8_t* bits = norms(field);
    bits[doc] = value;                    // set the value
  }


  char* SegmentReader::SegmentName(const char* ext, const int32_t x){
  //Func - Returns an allocated buffer in which it creates a filename by 
  //       concatenating segment with ext and x
  //Pre    ext != NULL and holds the extension
  //       x contains a number
  //Post - A buffer has been instantiated an when x = -1 buffer contains the concatenation of 
  //       segment and ext otherwise buffer contains the contentation of segment, ext and x
      
	  CND_PRECONDITION(ext     != NULL, "ext is NULL");

	  //Create a buffer of length CL_MAX_PATH
	  char* buf = _CL_NEWARRAY(char,CL_MAX_PATH);
	  //Create the filename
      SegmentName(buf,CL_MAX_PATH,ext,x);
	  
      return buf ;
  }

  void SegmentReader::SegmentName(char* buffer,int32_t bufferLen, const char* ext, const int32_t x ){
  //Func - Creates a filename in buffer by concatenating segment with ext and x
  //Pre  - buffer != NULL
  //       ext    != NULL
  //       x contains a number
  //Post - When x = -1 buffer contains the concatenation of segment and ext otherwise
  //       buffer contains the contentation of segment, ext and x

      CND_PRECONDITION(buffer  != NULL, "buffer is NULL");
      CND_PRECONDITION(segment != NULL, "Segment is NULL");

      Misc::segmentname(buffer,bufferLen,segment,ext,x);
  }
  void SegmentReader::openNorms(Directory* cfsDir) {
  //Func - Open all norms files for all fields
  //       Creates for each field a norm Instance with an open inputstream to 
  //       a corresponding norm file ready to be read
  //Pre  - true
  //Post - For each field a norm instance has been created with an open inputstream to
  //       a corresponding norm file ready to be read

      //Iterate through all the fields
      for (int32_t i = 0; i < fieldInfos->size(); i++) {
		  //Get the FieldInfo for the i-th field
          FieldInfo* fi = fieldInfos->fieldInfo(i);
          //Check if the field is indexed
          if (fi->isIndexed) {
		      //Allocate a buffer
              char fileName[CL_MAX_PATH];
			  //Create a filename for the norm file
              SegmentName(fileName,CL_MAX_PATH, ".f", fi->number);
              //TODO, should fi->name be copied?
			  //Create a new Norm with an open inputstream to f and store
			  //it at fi->name in norms
             Directory* d = getDirectory();
             if ( !d->fileExists(fileName) )
                d = cfsDir;

                 _norms.put(fi->name, _CLNEW Norm( d->openInput( fileName ),fi->number, this, segment ));
          }
      }
  }

  void SegmentReader::closeNorms() {
  //Func - Close all the norms stored in norms
  //Pre  - true
  //Post - All the norms have been destroyed

    SCOPED_LOCK_MUTEX(_norms.THIS_LOCK)
	//Create an interator initialized at the beginning of norms
	CL_NS(util)::CLHashtable<const TCHAR*,Norm*,Compare::TChar>::iterator itr = _norms.begin();
	//Iterate through all the norms
    while (itr != _norms.end()) {
        //Get the norm
        Norm* n = itr->second;
        //delete the norm n
        _CLDELETE(n);
        //Move the interator to the next norm in the norms collection.
	    //Note ++ is an overloaded operator
        itr++;
     }
    _norms.clear(); //bvk: they're deleted, so clear them so that they are not re-used
  }

    /**
   * @see IndexReader#getFieldNames()
   */
  TCHAR** SegmentReader::getFieldNames(){
    // maintain a unique set of field names
	int32_t len = fieldInfos->size();
    TCHAR** ret = _CL_NEWARRAY(TCHAR*,len+1);
	int32_t i = 0;
	int32_t p = 0;
    for (i = 0; i < len; i++) {
      FieldInfo* fi = fieldInfos->fieldInfo(i);
	   for ( int32_t j =0;j<i;j++ )
		  if ( _tcscmp(fi->name,ret[j]) == 0 )
			  continue;
      ret[p++]=STRDUP_TtoT(fi->name);
    }
    ret[p]=NULL;
    return ret;
  }

  /**
   * @see IndexReader#getFieldNames(boolean)
   */
  TCHAR** SegmentReader::getFieldNames(bool indexed) {
    // maintain a unique set of field names
    CL_NS(util)::CLSetList<const TCHAR*> fieldSet(false);
	int32_t i = 0;
    for (i = 0; i < fieldInfos->size(); i++) {
      FieldInfo* fi = fieldInfos->fieldInfo(i);
	   if (fi->isIndexed == indexed){
		 if ( fieldSet.find(fi->name)==fieldSet.end() )
			  fieldSet.insert(fi->name);
	   }
    }
    
	 TCHAR** ret = _CL_NEWARRAY(TCHAR*,fieldSet.size()+1);
    int j=0;
    CL_NS(util)::CLSetList<const TCHAR*>::iterator itr = fieldSet.begin();
    while ( itr != fieldSet.end() ){
        const TCHAR* t = *itr;
        ret[j]=STRDUP_TtoT(t);

        j++;
        itr++;
    }
    ret[fieldSet.size()]=NULL;
    return ret;
  }

  /**
   * 
   * @param storedTermVector if true, returns only Indexed fields that have term vector info, 
   *                        else only indexed fields without term vector info 
   * @return Collection of Strings indicating the names of the fields
   */
   TCHAR** SegmentReader::getIndexedFieldNames(bool storedTermVector) {
    // maintain a unique set of field names
    CL_NS(util)::CLSetList<const TCHAR*> fieldSet(false);
	 int32_t i = 0;
    for (i = 0; i < fieldInfos->size(); i++) {
      FieldInfo* fi = fieldInfos->fieldInfo(i);
      if (fi->isIndexed == true && fi->storeTermVector == storedTermVector){
		if ( fieldSet.find((const TCHAR*)fi->name)==fieldSet.end() )
			  fieldSet.insert(fi->name);
      }
    }
	 TCHAR** ret = _CL_NEWARRAY(TCHAR*,fieldSet.size()+1);
    int j=0;
    CL_NS(util)::CLSetList<const TCHAR*>::iterator itr = fieldSet.begin();
    while ( itr != fieldSet.end() ){
        const TCHAR* t = *itr;
        ret[j]=STRDUP_TtoT(t);

        j++;
        itr++;
    }
    ret[fieldSet.size()]=NULL;
    return ret;

  }

   TermFreqVector* SegmentReader::getTermFreqVector(int32_t docNumber, const TCHAR* field){
    // Check if this field is invalid or has no stored term vector
    FieldInfo* fi = fieldInfos->fieldInfo(field);
    if (fi == NULL || !fi->storeTermVector) 
       return NULL;

    return termVectorsReader->get(docNumber, field);
  }


   TermFreqVector** SegmentReader::getTermFreqVectors(int32_t docNumber){
    if (termVectorsReader == NULL)
      return NULL;

    return termVectorsReader->get(docNumber);
  }
CL_NS_END
