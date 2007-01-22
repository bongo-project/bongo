/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#include "CLucene/StdHeader.h"
#include "SegmentMerger.h"

CL_NS_USE(util)
CL_NS_USE(document)
CL_NS_USE(store)
CL_NS_DEF(index)

   // File extensions of old-style index files
   char* COMPOUND_EXTENSIONS="fnm\0" "frq\0" "prx\0" "fdx\0" "fdt\0" "tii\0" "tis\0";
   int COMPOUND_EXTENSIONS_LENGTH=7;

   char* VECTOR_EXTENSIONS="tvx\0" "tvd\0" "tvf\0";
   int VECTOR_EXTENSIONS_LENGTH=3;

  SegmentMerger::SegmentMerger(Directory* dir, const char* name, const bool compoundFile): directory(dir){
  //Func - Constructor
  //Pre  - dir holds a valid reference to a Directory
  //       name != NULL
  //Post - Instance has been created

      CND_PRECONDITION(name != NULL, "name is NULL");

      freqOutput       = NULL;
      proxOutput       = NULL;
      termInfosWriter  = NULL;
      queue            = NULL;
      segment          = STRDUP_AtoA(name);
      fieldInfos       = NULL;
      useCompoundFile  = compoundFile;
      skipBuffer       = _CLNEW CL_NS(store)::RAMIndexOutput();

	  lastSkipDoc=0;
	  lastSkipFreqPointer=0;
	  lastSkipProxPointer=0;
	  skipInterval=0;
  }

  SegmentMerger::~SegmentMerger(){
  //Func - Destructor
  //Pre  - true
  //Post - The instance has been destroyed
      
	  //Clear the readers set
	  readers.clear();

	  //Delete field Infos
    _CLDELETE(fieldInfos);     
    //Close and destroy the IndexOutput to the Frequency File
    if (freqOutput != NULL){ 
		  freqOutput->close(); 
		  _CLDELETE(freqOutput); 
	  }
    //Close and destroy the IndexOutput to the Prox File
    if (proxOutput != NULL){
		  proxOutput->close(); 
		  _CLDELETE(proxOutput); 
    }
    //Close and destroy the termInfosWriter
    if (termInfosWriter != NULL){
		  termInfosWriter->close(); 
		  _CLDELETE(termInfosWriter); 
    }
    //Close and destroy the queue
    if (queue != NULL){
		  queue->close(); 
		  _CLDELETE(queue);
	  }
	  //close and destory the skipBuffer
	  if ( skipBuffer != NULL ){
			skipBuffer->close();
			_CLDELETE(skipBuffer);
	  }

	  _CLDELETE_CaARRAY(segment);
  }

  void SegmentMerger::add(SegmentReader* reader) {
  //Func - Adds a SegmentReader to the set of readers
  //Pre  - reader contains a valid reference to a SegmentReader
  //Post - The SegementReader reader has been added to the set of readers

      readers.push_back(reader);
  }

  IndexReader* SegmentMerger::segmentReader(const int32_t i) {
  //Func - Returns a reference to the i-th SegmentReader
  //Pre  - 0 <= i < readers.size()
  //Post - A reference to the i-th SegmentReader has been returned

	  CND_PRECONDITION(i >= 0, "i is a negative number");
      CND_PRECONDITION((size_t)i < readers.size(), "i is bigger than the number of SegmentReader instances");

	  //Retrieve the i-th SegmentReader
      SegmentReader* ret = readers[i];
      CND_CONDITION(ret != NULL,"No SegmentReader found");

      return ret;
  }

  int32_t SegmentMerger::merge() {
    int32_t value = mergeFields();
    mergeTerms();
    mergeNorms();

    if (fieldInfos->hasVectors())
      mergeVectors();

    if (useCompoundFile)
      createCompoundFile();

    return value;
  }

  void SegmentMerger::closeReaders(){
    for (uint32_t i = 0; i < readers.size(); i++) {  // close readers
      IndexReader* reader = readers[i];
      reader->close();
    }
  }

  void SegmentMerger::createCompoundFile(){
      char name[CL_MAX_PATH];
      _snprintf(name,CL_MAX_PATH,"%s.cfs",segment);
      CompoundFileWriter* cfsWriter = _CLNEW CompoundFileWriter(directory, name);


      char** files = _CL_NEWARRAY(char*, COMPOUND_EXTENSIONS_LENGTH  + VECTOR_EXTENSIONS_LENGTH + fieldInfos->size());
      int32_t fileslen = 0;

	  { //msvc6 scope fix
		  // Basic files
		  for (int32_t i = 0; i < COMPOUND_EXTENSIONS_LENGTH; i++) {
			 files[fileslen]=Misc::ajoin(segment,".",COMPOUND_EXTENSIONS+(i*4));
			 fileslen++;
		  }
	  }

	  { //msvc6 scope fix
		  // Field norm files
		  for (int32_t i = 0; i < fieldInfos->size(); i++) {
			 FieldInfo* fi = fieldInfos->fieldInfo(i);
			 if (fi->isIndexed) {
				TCHAR tbuf[10];
				char abuf[10];
				_i64tot(i,tbuf,10);
				STRCPY_TtoA(abuf,tbuf,10);

				files[fileslen] = Misc::ajoin(segment,".f",abuf);
				fileslen++;
			 }
		  }
	  }

      // Vector files
      if (fieldInfos->hasVectors()) {
         for (int32_t i = 0; i < VECTOR_EXTENSIONS_LENGTH; i++) {
            files[fileslen] = Misc::ajoin(segment, ".", VECTOR_EXTENSIONS+(i*4));
					  fileslen++;
         }
      }

	{ //msvc6 scope fix
		// Now merge all added files
		for ( int32_t i=0;i<fileslen;i++ ){
		  cfsWriter->addFile(files[i]);
		}
	}
    
    // Perform the merge
    cfsWriter->close();
	_CLDELETE(cfsWriter);
        
	{ //msvc6 scope fix
		// Now delete the source files
		for ( int32_t i=0;i<fileslen;i++ ){
		  directory->deleteFile(files[i]);
		  _CLDELETE_LCaARRAY(files[i]);
		}
	}

    _CLDELETE_ARRAY(files);
  }

  int32_t SegmentMerger::mergeFields() {
  //Func - Merge the fields of all segments 
  //Pre  - true
  //Post - The field infos and field values of all segments have been merged.
		
	  //Create a new FieldInfos
      fieldInfos = _CLNEW FieldInfos();		  // merge field names

      //Condition check to see if fieldInfos points to a valid instance
      CND_CONDITION(fieldInfos != NULL,"Memory allocation for fieldInfos failed");

	  SegmentReader* reader = NULL;

     int32_t docCount = 0;

     //Iterate through all readers
     for (uint32_t i = 0; i < readers.size(); i++){
          //get the i-th reader
          reader = readers[i];
          //Condition check to see if reader points to a valid instance
          CND_CONDITION(reader != NULL,"No SegmentReader found");
		    

		  TCHAR** tmp = NULL;

		  tmp = reader->getIndexedFieldNames(true);
          fieldInfos->add((const TCHAR**)tmp, true, true);
		  _CLDELETE_CARRAY_ALL(tmp);
		
		  tmp = reader->getIndexedFieldNames(false);
          fieldInfos->add((const TCHAR**)tmp, true, false);
          _CLDELETE_CARRAY_ALL(tmp);

		  tmp = reader->getFieldNames(false);
          fieldInfos->add((const TCHAR**)tmp, false, false);
		  _CLDELETE_CARRAY_ALL(tmp);
     }
		
	  //Create the filename of the new FieldInfos file
	  const char* buf = Misc::segmentname(segment,".fnm");
	  //Write the new FieldInfos file to the directory
      fieldInfos->write(directory, buf );
	  //Destroy the buffer of the filename
      _CLDELETE_CaARRAY(buf);
	    
	  // merge field values


	  //Instantiate Fieldswriter which will write in directory for the segment name segment
      //Using the new merged fieldInfos
      FieldsWriter* fieldsWriter = _CLNEW FieldsWriter(directory, segment, fieldInfos);
      
	  //Condition check to see if fieldsWriter points to a valid instance
      CND_CONDITION(fieldsWriter != NULL,"Memory allocation for fieldsWriter failed");

      try {  
          IndexReader* reader = NULL;
		    int32_t maxDoc          = 0;
          //Iterate through all readers
          for (uint32_t i = 0; i < readers.size(); i++) {
              //get the i-th reader
              reader = (SegmentReader*)readers[i];


			  //Condition check to see if reader points to a valid instance
              CND_CONDITION(reader != NULL, "No SegmentReader found");

			  //Get the total number documents including the documents that have been marked deleted
              int32_t maxDoc = reader->maxDoc();
                  
			  //Iterate through all the documents managed by the current reader
			  for (int32_t j = 0; j < maxDoc; j++){
				  //Check if the j-th document has been deleted, if so skip it
				  if (!reader->isDeleted(j)){ 
					//Get the document
					Document* doc = reader->document(j);
					//Add the document to the new FieldsWriter
					fieldsWriter->addDocument( doc );
					docCount++;
					//doc is not used anymore so have it deleted
					_CLDELETE(doc);
				  }
			  }
		  }
	  }_CLFINALLY(
		  //Close the fieldsWriter
          fieldsWriter->close();
	      //And have it deleted as it not used any more
          _CLDELETE( fieldsWriter );
      );

      return docCount;
  }

  void SegmentMerger::mergeVectors(){
    TermVectorsWriter* termVectorsWriter = 
      _CLNEW TermVectorsWriter(directory, segment, fieldInfos);

    try {
      for (uint32_t r = 0; r < readers.size(); r++) {
        IndexReader* reader = readers[r];
        int32_t maxDoc = reader->maxDoc();
        for (int32_t docNum = 0; docNum < maxDoc; docNum++) {
          // skip deleted docs
          if (reader->isDeleted(docNum)) {
            continue;
          }
          termVectorsWriter->openDocument();

          // get all term vectors
          TermFreqVector** sourceTermVector =
            reader->getTermFreqVectors(docNum);

          if (sourceTermVector != NULL) {
            int32_t f = 0;
            TermFreqVector* termVector=NULL;
            while ( (termVector=sourceTermVector[f++]) != NULL ){
              termVectorsWriter->openField(termVector->getField());
              const TCHAR** terms = termVector->getTerms();
              const int32_t* freqs = termVector->getTermFrequencies();
              
              int32_t t = 0;
              while ( terms[t] != NULL ){
                termVectorsWriter->addTerm(terms[t], freqs[t]);
                //todo: delete terms string return
                t++;
              }

              _CLDELETE(termVector);
            }
            _CLDELETE_ARRAY(sourceTermVector);
          }
          termVectorsWriter->closeDocument();
        }
      }
    }_CLFINALLY( _CLDELETE(termVectorsWriter); );
  }


  void SegmentMerger::mergeTerms() {
  //Func - Merge the terms of all segments
  //Pre  - fieldInfos != NULL
  //Post - The terms of all segments have been merged

	  CND_PRECONDITION(fieldInfos != NULL, "fieldInfos is NULL");

      try{
		  //create a filename for the new Frequency File for segment
          const char* buf = Misc::segmentname(segment,".frq");
		  //Open an IndexOutput to the new Frequency File
          freqOutput = directory->createOutput( buf );
          //Destroy the buffer of the filename
          _CLDELETE_CaARRAY(buf);
		
		  //create a filename for the new Prox File for segment
          buf = Misc::segmentname(segment,".prx");
		  //Open an IndexOutput to the new Prox File
          proxOutput = directory->createOutput( buf );
		  //delete buffer
          _CLDELETE_CaARRAY( buf );
		
		  //Instantiate  a new termInfosWriter which will write in directory
		  //for the segment name segment using the new merged fieldInfos
          termInfosWriter = _CLNEW TermInfosWriter(directory, segment, fieldInfos);  
          
          //Condition check to see if termInfosWriter points to a valid instance
          CND_CONDITION(termInfosWriter != NULL,"Memory allocation for termInfosWriter failed")	;
          
		  skipInterval = termInfosWriter->skipInterval;
          queue = _CLNEW SegmentMergeQueue(readers.size());


		  //And merge the Term Infos
          mergeTermInfos();	      
      }_CLFINALLY(
		  //Close and destroy the IndexOutput to the Frequency File
          if (freqOutput != NULL) 		{ freqOutput->close(); _CLDELETE(freqOutput); }
          //Close and destroy the IndexOutput to the Prox File
          if (proxOutput != NULL) 		{ proxOutput->close(); _CLDELETE(proxOutput); }
		  //Close and destroy the termInfosWriter
          if (termInfosWriter != NULL) 	{ termInfosWriter->close(); _CLDELETE(termInfosWriter); }
		  //Close and destroy the queue
          if (queue != NULL)            { queue->close(); _CLDELETE(queue);}
	  );
  }

  void SegmentMerger::mergeTermInfos(){
  //Func - Merges all TermInfos into a single segment
  //Pre  - true
  //Post - All TermInfos have been merged into a single segment

      //Condition check to see if queue points to a valid instance
      CND_CONDITION(queue != NULL, "Memory allocation for queue failed")	;

	  //base is the id of the first document in a segment
      int32_t base = 0;

      IndexReader* reader = NULL;
	   SegmentMergeInfo* smi = NULL;

	  //iterate through all the readers
      for (uint32_t i = 0; i < readers.size(); i++) {
		  //Get the i-th reader
          reader = readers[i];

          //Condition check to see if reader points to a valid instance
          CND_CONDITION(reader != NULL, "No SegmentReader found");

		    //Get the term enumeration of the reader
          TermEnum* termEnum = reader->terms();
          //Instantiate a new SegmentMerginfo for the current reader and enumeration
          smi = _CLNEW SegmentMergeInfo(base, termEnum, reader);

          //Condition check to see if smi points to a valid instance
          CND_CONDITION(smi != NULL, "Memory allocation for smi failed")	;

		  //Increase the base by the number of documents that have not been marked deleted
		  //so base will contain a new value for the first document of the next iteration
          base += reader->numDocs();
		  //Get the next current term
		  if (smi->next()){
              //Store the SegmentMergeInfo smi with the initialized SegmentTermEnum TermEnum
			  //into the queue
              queue->put(smi);
          }else{
			  //Apparently the end of the TermEnum of the SegmentTerm has been reached so
			  //close the SegmentMergeInfo smi
              smi->close();
			  //And destroy the instance and set smi to NULL (It will be used later in this method)
              _CLDELETE(smi);
              }
          }

	  //Instantiate an array of SegmentMergeInfo instances called match
      SegmentMergeInfo** match = _CL_NEWARRAY(SegmentMergeInfo*,readers.size()+1);

      //Condition check to see if match points to a valid instance
      CND_CONDITION(match != NULL, "Memory allocation for match failed")	;
     
      SegmentMergeInfo* top = NULL;

      //As long as there are SegmentMergeInfo instances stored in the queue
      while (queue->size() > 0) {
          int32_t matchSize = 0;			  
		  
		  // pop matching terms
          
		  //Pop the first SegmentMergeInfo from the queue
          match[matchSize++] = queue->pop();
		  //Get the Term of match[0]
          Term* term = match[0]->term;
			  
          //Condition check to see if term points to a valid instance
          CND_CONDITION(term != NULL,"term is NULL")	;

          //Get the current top of the queue
		  top = queue->top();

          //For each SegmentMergInfo still in the queue 
		  //Check if term matches the term of the SegmentMergeInfo instances in the queue
          while (top != NULL && term->equals(top->term) ){ //todo: changed to equals, but check if this is more efficient
			  //A match has been found so add the matching SegmentMergeInfo to the match array
              match[matchSize++] = queue->pop();
			  //Get the next SegmentMergeInfo
              top = queue->top();
          }
		  match[matchSize]=NULL;

		  //add new TermInfo
          mergeTermInfo(match); //matchSize  
		  
          //Restore the SegmentTermInfo instances in the match array back into the queue
          while (matchSize > 0){
              smi = match[--matchSize];
			  
              //Condition check to see if smi points to a valid instance
              CND_CONDITION(smi != NULL,"smi is NULL")	;

			  //Move to the next term in the enumeration of SegmentMergeInfo smi
			  if (smi->next()){
                  //There still are some terms so restore smi in the queue
                  queue->put(smi);
				  
              }else{
				  //Done with a segment
				  //No terms anymore so close this SegmentMergeInfo instance
                  smi->close();				  
                  _CLDELETE( smi );
              }
          }
     }

     _CLDELETE_ARRAY(match);
  }

  void SegmentMerger::mergeTermInfo( SegmentMergeInfo** smis){
  //Func - Merge the TermInfo of a term found in one or more segments. 
  //Pre  - smis != NULL and it contains segments that are positioned at the same term.
  //       n is equal to the number of SegmentMergeInfo instances in smis
  //       freqOutput != NULL
  //       proxOutput != NULL
  //Post - The TermInfo of a term has been merged

	  CND_PRECONDITION(smis != NULL, "smis is NULL");
	  CND_PRECONDITION(freqOutput != NULL, "freqOutput is NULL");
	  CND_PRECONDITION(proxOutput != NULL, "proxOutput is NULL");

	  //Get the file pointer of the IndexOutput to the Frequency File
      int64_t freqPointer = freqOutput->getFilePointer();
	  //Get the file pointer of the IndexOutput to the Prox File
      int64_t proxPointer = proxOutput->getFilePointer();

      //Process postings from multiple segments all positioned on the same term.
      int32_t df = appendPostings(smis);  

      int64_t skipPointer = writeSkip();

	  //df contains the number of documents across all segments where this term was found
      if (df > 0) {
          //add an entry to the dictionary with pointers to prox and freq files
          termInfo.set(df, freqPointer, proxPointer, (int32_t)(skipPointer - freqPointer));
          //Precondition check for to be sure that the reference to
		  //smis[0]->term will be valid
          CND_PRECONDITION(smis[0]->term != NULL, "smis[0]->term is NULL");
		  //Write a new TermInfo
          termInfosWriter->add(smis[0]->term, &termInfo);
       }
  }
	       
  
  int32_t SegmentMerger::appendPostings(SegmentMergeInfo** smis){
  //Func - Process postings from multiple segments all positioned on the
  //       same term. Writes out merged entries into freqOutput and
  //       the proxOutput streams.
  //Pre  - smis != NULL and it contains segments that are positioned at the same term.
  //       n is equal to the number of SegmentMergeInfo instances in smis
  //       freqOutput != NULL
  //       proxOutput != NULL
  //Post - Returns number of documents across all segments where this term was found

      CND_PRECONDITION(smis != NULL, "smis is NULL");
	  CND_PRECONDITION(freqOutput != NULL, "freqOutput is NULL");
	  CND_PRECONDITION(proxOutput != NULL, "proxOutput is NULL");

      int32_t lastDoc = 0;
      int32_t df = 0;       //Document Counter

       resetSkip();
      SegmentMergeInfo* smi = NULL;

	  //Iterate through all SegmentMergeInfo instances in smis
      int32_t i = 0;
      while ( (smi=smis[i]) != NULL ){
		  //Get the i-th SegmentMergeInfo 

          //Condition check to see if smi points to a valid instance
		  CND_PRECONDITION(smi!=NULL,"	 is NULL");

		  //Get the term positions 
          TermPositions* postings = smi->postings;
		  //Get the base of this segment
          int32_t base = smi->base;
		  //Get the docMap so we can see which documents have been deleted
          int32_t* docMap = smi->docMap;
		    //Seek the termpost
          postings->seek(smi->termEnum);
          while (postings->next()) {
           int32_t doc = postings->doc();
			  //Check if there are deletions
			  if (docMap != NULL)
				  doc = docMap[doc]; // map around deletions
           doc += base;                              // convert to merged space

            //Condition check to see doc is eaqual to or bigger than lastDoc
            CND_CONDITION(doc >= lastDoc,"docs out of order");

			   //Increase the total frequency over all segments
            df++;

            if ((df % skipInterval) == 0) {
               bufferSkip(lastDoc);
            }

			  //Calculate a new docCode 
			  //use low bit to flag freq=1
            int32_t docCode = (doc - lastDoc) << 1;	  
            lastDoc = doc;

			  //Get the frequency of the Term
              int32_t freq = postings->freq();
              if (freq == 1){
                  //write doc & freq=1
                  freqOutput->writeVInt(docCode | 1);	  
              }else{
				  //write doc
                  freqOutput->writeVInt(docCode);	  
				  //write frequency in doc
                  freqOutput->writeVInt(freq);		  
              }
				  
			  int32_t lastPosition = 0;			  
			  // write position deltas
			  for (int32_t j = 0; j < freq; j++) {
				  //Get the next position
                  int32_t position = postings->nextPosition();
				  //Write the difference between position and the last position
                  proxOutput->writeVInt(position - lastPosition);			  
                  lastPosition = position;
             }
          }

          i++;
      }

      //Return total number of documents across all segments where term was found		
      return df;
  }

  void SegmentMerger::resetSkip(){
    skipBuffer->reset();
    lastSkipDoc = 0;
    lastSkipFreqPointer = freqOutput->getFilePointer();
    lastSkipProxPointer = proxOutput->getFilePointer();
  }

  void SegmentMerger::bufferSkip(int32_t doc){
    int64_t freqPointer = freqOutput->getFilePointer();
    int64_t proxPointer = proxOutput->getFilePointer();

    skipBuffer->writeVInt(doc - lastSkipDoc);
    skipBuffer->writeVInt((int32_t) (freqPointer - lastSkipFreqPointer));
    skipBuffer->writeVInt((int32_t) (proxPointer - lastSkipProxPointer));

    lastSkipDoc = doc;
    lastSkipFreqPointer = freqPointer;
    lastSkipProxPointer = proxPointer;
  }

  int64_t SegmentMerger::writeSkip(){
    int64_t skipPointer = freqOutput->getFilePointer();
    skipBuffer->writeTo(freqOutput);
    return skipPointer;
  }

  void SegmentMerger::mergeNorms() {
  //Func - Merges the norms for all fields 
  //Pre  - fieldInfos != NULL
  //Post - The norms for all fields have been merged

      CND_PRECONDITION(fieldInfos != NULL, "fieldInfos is NULL");

	  IndexReader* reader  = NULL;
	  IndexOutput*  output  = NULL;

	  //iterate through all the Field Infos instances
      for (int32_t i = 0; i < fieldInfos->size(); i++) {
		  //Get the i-th FieldInfo
          FieldInfo* fi = fieldInfos->fieldInfo(i);
		  //Is this Field indexed?
          if (fi->isIndexed){
			  //Create an new filename for the norm file
              const char* buf = Misc::segmentname(segment,".f", i);
			  //Instantiate  an IndexOutput to that norm file
              output = directory->createOutput( buf );

			  //Condition check to see if output points to a valid instance
              CND_CONDITION(output != NULL, "No Outputstream retrieved");

              //Destroy the buffer of the filename
              _CLDELETE_CaARRAY( buf );
              
			  try{
				  //Iterate throug all SegmentReaders
                  for (uint32_t j = 0; j < readers.size(); j++) {
					  //Get the i-th IndexReader
                      reader = readers[j];

                      //Condition check to see if reader points to a valid instance
                      CND_CONDITION(reader != NULL, "No reader found");

					  //Get an IndexInput to the norm file for this field in this segment
                      uint8_t* input = reader->norms(fi->name);

					  //Get the total number of documents including the documents that have been marked deleted
                      int32_t maxDoc = reader->maxDoc();
						  //Iterate through all the documents
                          for(int32_t k = 0; k < maxDoc; k++) {
                              //Get the norm
							  //Note that the byte must always be read especially when the document
							  //is marked deleted to remain in sync
                              uint8_t norm = input != NULL ? input[k] : 0;

                              //Check if document k is deleted
						      if (!reader->isDeleted(k)){
								  //write the new norm
                                  output->writeByte(norm);
                                  }
                              }
                          } 
                      
                  }
              _CLFINALLY(
				  if (output != NULL){
				      //Close the IndexOutput output
                      output->close();
			          //destroy it
                      _CLDELETE(output);
				      }
				  );
			  }
		 }
  }

CL_NS_END
