/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#include "CLucene/StdHeader.h"
#include "FieldSortedHitQueue.h"
#include "Compare.h"

CL_NS_USE(util)
CL_NS_USE(index)
CL_NS_DEF(search)
		
FieldSortedHitQueue::hitqueueCacheType FieldSortedHitQueue::Comparators(false,true);

FieldSortedHitQueue::FieldSortedHitQueue (IndexReader* reader, SortField** fields, int32_t size):
	maxscore(1.0f),
	fieldsLen(0)
{
	while ( fields[fieldsLen] != 0 )
		fieldsLen++;

	comparators = _CL_NEWARRAY(ScoreDocComparator*,fieldsLen+1);
	this->fields = _CL_NEWARRAY(SortField*,fieldsLen+1);
	for (int32_t i=0; i<fieldsLen; ++i) {
		const TCHAR* fieldname = fields[i]->getField();
		//todo: fields[i].getLocale(), not implemented
		comparators[i] = getCachedComparator (reader, fieldname, fields[i]->getType(), fields[i]->getFactory());
		this->fields[i] = _CLNEW SortField (fieldname, comparators[i]->sortType(), fields[i]->getReverse());
	}
	comparatorsLen = fieldsLen;
	comparators[fieldsLen]=NULL;
	this->fields[fieldsLen] = NULL;

	initialize(size,true);
}

//static
ScoreDocComparator* FieldSortedHitQueue::comparatorString (IndexReader* reader, const TCHAR* field) {
	//const TCHAR* field = CLStringIntern::intern(fieldname CL_FILELINE);
	FieldCacheAuto* fa = FieldCache::DEFAULT->getStringIndex (reader, field);
	//CLStringIntern::unintern(field);

	CND_PRECONDITION(fa->contentType==FieldCacheAuto::STRING_INDEX,"Content type is incorrect");
	fa->ownContents = false;
    return _CLNEW ScoreDocComparators::String(fa->stringIndex, fa->contentLen);
}

//static 
ScoreDocComparator* FieldSortedHitQueue::comparatorInt (IndexReader* reader, const TCHAR* field){
    //const TCHAR* field = CLStringIntern::intern(fieldname CL_FILELINE);
    FieldCacheAuto* fa =  FieldCache::DEFAULT->getInts (reader, field);
	//CLStringIntern::unintern(field);

	CND_PRECONDITION(fa->contentType==FieldCacheAuto::INT_ARRAY,"Content type is incorrect");
    return _CLNEW ScoreDocComparators::Int32(fa->intArray, fa->contentLen);
  }

//static
 ScoreDocComparator* FieldSortedHitQueue::comparatorFloat (IndexReader* reader, const TCHAR* field) {
	//const TCHAR* field = CLStringIntern::intern(fieldname CL_FILELINE);
    FieldCacheAuto* fa = FieldCache::DEFAULT->getFloats (reader, field);
	//CLStringIntern::unintern(field);

	CND_PRECONDITION(fa->contentType==FieldCacheAuto::FLOAT_ARRAY,"Content type is incorrect");
	return _CLNEW ScoreDocComparators::Float (fa->floatArray, fa->contentLen);
  }
//static
  ScoreDocComparator* FieldSortedHitQueue::comparatorAuto (IndexReader* reader, const TCHAR* field){
	//const TCHAR* field = CLStringIntern::intern(fieldname CL_FILELINE);
    FieldCacheAuto* fa =  FieldCache::DEFAULT->getAuto (reader, field);
	//CLStringIntern::unintern(field);

    if (fa->contentType == FieldCacheAuto::STRING_INDEX ) {
      return comparatorString (reader, field);
    } else if (fa->contentType == FieldCacheAuto::INT_ARRAY) {
      return comparatorInt (reader, field);
    } else if (fa->contentType == FieldCacheAuto::FLOAT_ARRAY) {
      return comparatorFloat (reader, field);
    } else if (fa->contentType == FieldCacheAuto::STRING_ARRAY) {
      return comparatorString (reader, field);
    } else {
      _CLTHROWA(CL_ERR_Runtime, "unknown data type in field"); //todo: rich error information: '"+field+"'");
    }
  }


  //todo: Locale locale, not implemented yet
  ScoreDocComparator* FieldSortedHitQueue::getCachedComparator (IndexReader* reader, const TCHAR* fieldname, int32_t type, SortComparatorSource* factory){ 
	if (type == SortField::DOC) 
		return ScoreDocComparator::INDEXORDER;
	if (type == SortField::DOCSCORE) 
		return ScoreDocComparator::RELEVANCE;
    ScoreDocComparator* comparator = lookup (reader, fieldname, type, factory);
    if (comparator == NULL) {
      switch (type) {
		case SortField::AUTO:
          comparator = comparatorAuto (reader, fieldname);
          break;
		case SortField::INT:
          comparator = comparatorInt (reader, fieldname);
          break;
		case SortField::FLOAT:
          comparator = comparatorFloat (reader, fieldname);
          break;
		case SortField::STRING:
          //if (locale != NULL) 
		//	  comparator = comparatorStringLocale (reader, fieldname, locale);
          //else 
			  comparator = comparatorString (reader, fieldname);
          break;
		case SortField::CUSTOM:
          comparator = factory->newComparator (reader, fieldname);
          break;
        default:
          _CLTHROWA(CL_ERR_Runtime,"unknown field type");
		  //todo: extend error
			//throw _CLNEW RuntimeException ("unknown field type: "+type);
      }
      store (reader, fieldname, type, factory, comparator);
    }
	return comparator;
  }
  
  
  FieldDoc* FieldSortedHitQueue::fillFields (FieldDoc* doc) {
    int32_t n = comparatorsLen;
    Comparable** fields = _CL_NEWARRAY(Comparable*,n+1);
    for (int32_t i=0; i<n; ++i)
      fields[i] = comparators[i]->sortValue(doc);
	fields[n]=NULL;
    doc->fields = fields;
    if (maxscore > 1.0f) 
        doc->score /= maxscore;   // normalize scores
    return doc;
  }

  ScoreDocComparator* FieldSortedHitQueue::lookup (IndexReader* reader, const TCHAR* field, int32_t type, SortComparatorSource* factory) {
    ScoreDocComparator* sdc = NULL;
    FieldCacheImpl::FileEntry* entry = (factory != NULL)
	  ? _CLNEW FieldCacheImpl::FileEntry (field, factory)
      : _CLNEW FieldCacheImpl::FileEntry (field, type);
	
	{
		SCOPED_LOCK_MUTEX(Comparators.THIS_LOCK)
		hitqueueCacheReaderType* readerCache = Comparators.get(reader);
		if (readerCache == NULL){
			_CLDELETE(entry);
			return NULL;
		}
		
		sdc = readerCache->get (entry);
		_CLDELETE(entry);
	}
	return sdc;
  }

	void FieldSortedHitQueue::closeCallback(CL_NS(index)::IndexReader* reader, void* param){
		SCOPED_LOCK_MUTEX(Comparators.THIS_LOCK)
		Comparators.remove(reader);
	}
	
  //static
  void FieldSortedHitQueue::store (IndexReader* reader, const TCHAR* field, int32_t type, SortComparatorSource* factory, ScoreDocComparator* value) {
	FieldCacheImpl::FileEntry* entry = (factory != NULL)
		? _CLNEW FieldCacheImpl::FileEntry (field, factory)
		: _CLNEW FieldCacheImpl::FileEntry (field, type);

	{
		SCOPED_LOCK_MUTEX(Comparators.THIS_LOCK)
		hitqueueCacheReaderType* readerCache = Comparators.get(reader);
		if (readerCache == NULL) {
			readerCache = _CLNEW hitqueueCacheReaderType(true);
			Comparators.put(reader,readerCache);
			reader->addCloseCallback(FieldSortedHitQueue::closeCallback,NULL);
		}
		readerCache->put (entry, value);
		//return NULL; //supposed to return previous value...
	}
  }

  FieldSortedHitQueue::~FieldSortedHitQueue(){
      _CLDELETE_ARRAY(comparators);
  }
CL_NS_END
