package java

// jdkMethodReturns maps ClassName.methodName → return type for common JDK methods.
// Consulted by resolveChainedReceiver when SymbolTable lookup fails.
//
// Return type conventions:
//   concrete type (e.g. "Stream"): returns that type
//   "SELF": returns receiver type (builder/fluent)
//   "T": returns container's first generic type arg
//   "V": returns Map's value type arg
//   "": terminal operation, chain ends
var jdkMethodReturns = map[string]string{
	// Collection / List / Set
	"List.stream": "Stream", "List.iterator": "Iterator", "List.get": "T",
	"List.set": "T", "List.remove": "T", "List.subList": "List",
	"List.toArray": "Object[]", "List.size": "", "List.isEmpty": "",
	"List.contains": "", "List.add": "", "List.indexOf": "",
	"Set.stream": "Stream", "Set.iterator": "Iterator", "Set.toArray": "Object[]",
	"Collection.stream": "Stream", "Collection.iterator": "Iterator", "Collection.toArray": "Object[]",
	"ArrayList.stream": "Stream", "LinkedList.stream": "Stream",

	// Stream
	"Stream.filter": "Stream", "Stream.map": "Stream", "Stream.flatMap": "Stream",
	"Stream.sorted": "Stream", "Stream.distinct": "Stream", "Stream.peek": "Stream",
	"Stream.limit": "Stream", "Stream.skip": "Stream",
	"Stream.collect": "", "Stream.toList": "List", "Stream.forEach": "",
	"Stream.findFirst": "Optional", "Stream.findAny": "Optional",
	"Stream.reduce": "Optional", "Stream.min": "Optional", "Stream.max": "Optional",
	"Stream.count": "", "Stream.toArray": "",

	// Optional
	"Optional.map": "Optional", "Optional.flatMap": "Optional", "Optional.filter": "Optional",
	"Optional.orElse": "T", "Optional.orElseGet": "T", "Optional.orElseThrow": "T",
	"Optional.get": "T", "Optional.ifPresent": "", "Optional.isPresent": "",
	"Optional.stream": "Stream",

	// Map
	"Map.get": "V", "Map.getOrDefault": "V", "Map.put": "V", "Map.remove": "V",
	"Map.entrySet": "Set", "Map.keySet": "Set", "Map.values": "Collection",
	"HashMap.get": "V", "HashMap.put": "V", "LinkedHashMap.get": "V",
	"ConcurrentHashMap.get": "V", "TreeMap.get": "V",
	"Entry.getKey": "T", "Entry.getValue": "V",

	// Iterator
	"Iterator.next": "T", "Iterator.hasNext": "",

	// Future
	"Future.get": "T", "CompletableFuture.get": "T", "CompletableFuture.join": "T",
	"CompletableFuture.thenApply": "CompletableFuture",

	// StringBuilder
	"StringBuilder.append": "SELF", "StringBuilder.insert": "SELF",
	"StringBuilder.reverse": "SELF", "StringBuilder.toString": "String",
	"StringBuffer.append": "SELF", "StringBuffer.toString": "String",

	// String
	"String.trim": "String", "String.strip": "String",
	"String.toLowerCase": "String", "String.toUpperCase": "String",
	"String.substring": "String", "String.replace": "String",
	"String.replaceAll": "String", "String.valueOf": "String",
	"String.format": "String", "String.toString": "String",
	"String.length": "", "String.equals": "", "String.contains": "",
	"String.startsWith": "", "String.endsWith": "", "String.isEmpty": "",
	"String.charAt": "", "String.split": "",

	// Object
	"Object.toString": "String", "Object.getClass": "Class",
	"Object.hashCode": "", "Object.equals": "",
	"Object.clone": "Object", "Object.notify": "", "Object.notifyAll": "",
	"Object.wait": "", "Object.finalize": "",

	// Throwable / Exception
	"Throwable.getMessage":       "String",
	"Throwable.getLocalizedMessage": "String",
	"Throwable.getCause":         "Throwable",
	"Throwable.getStackTrace":    "",
	"Throwable.toString":         "String",
	"Throwable.printStackTrace":  "",
	"Throwable.initCause":        "Throwable",
	"Throwable.getSuppressed":    "",
	"Throwable.addSuppressed":    "",
	"Exception.getMessage":       "String",
	"Exception.getCause":         "Throwable",
	"Exception.getStackTrace":    "",
	"Exception.toString":         "String",
	"RuntimeException.getMessage": "String",

	// Class
	"Class.getName":          "String",
	"Class.getSimpleName":    "String",
	"Class.getCanonicalName": "String",
	"Class.getSuperclass":    "Class",
	"Class.getInterfaces":    "",
	"Class.isInstance":       "",
	"Class.isAssignableFrom": "",
	"Class.newInstance":      "Object",
	"Class.forName":          "Class",
	"Class.cast":             "Object",
	"Class.getMethod":        "",
	"Class.getDeclaredField": "",

	// Number
	"Number.intValue":    "",
	"Number.longValue":   "",
	"Number.floatValue":  "",
	"Number.doubleValue": "",
	"Integer.parseInt":    "",
	"Integer.valueOf":     "Integer",
	"Integer.intValue":    "",
	"Integer.toString":    "String",
	"Long.parseLong":      "",
	"Long.valueOf":        "Long",
	"Long.longValue":      "",
	"Long.toString":       "String",
	"Double.parseDouble":  "",
	"Double.valueOf":      "Double",
	"Double.toString":     "String",
	"Float.parseFloat":    "",
	"Float.valueOf":       "Float",
	"Boolean.parseBoolean": "",
	"Boolean.valueOf":     "Boolean",
	"Boolean.booleanValue": "",
	"BigDecimal.add":       "BigDecimal",
	"BigDecimal.subtract":  "BigDecimal",
	"BigDecimal.multiply":  "BigDecimal",
	"BigDecimal.divide":    "BigDecimal",
	"BigDecimal.compareTo": "",
	"BigDecimal.toString":  "String",
	"BigDecimal.intValue":  "",
	"BigDecimal.longValue": "",
	"BigDecimal.doubleValue": "",
	"BigDecimal.setScale":  "BigDecimal",
	"BigDecimal.stripTrailingZeros": "BigDecimal",

	// Enum
	"Enum.name":    "String",
	"Enum.ordinal": "",
	"Enum.valueOf": "SELF",

	// Comparable
	"Comparable.compareTo": "",

	// Iterable
	"Iterable.iterator": "Iterator",
	"Iterable.forEach":  "",

	// Date / Time
	"Date.getTime":   "",
	"Date.toString":  "String",
	"Date.before":    "",
	"Date.after":     "",
	"Instant.now":    "Instant",
	"Instant.toEpochMilli": "",
	"LocalDate.now":  "LocalDate",
	"LocalDate.toString": "String",
	"LocalDateTime.now": "LocalDateTime",
	"LocalDateTime.toString": "String",
	"System.currentTimeMillis": "",
	"System.nanoTime": "",
	"System.getenv":   "String",
	"System.getProperty": "String",

	// Math
	"Math.max":   "",
	"Math.min":   "",
	"Math.abs":   "",
	"Math.round": "",
	"Math.ceil":  "",
	"Math.floor": "",
	"Math.random": "",

	// Pattern / Matcher
	"Pattern.compile": "Pattern",
	"Pattern.matcher": "Matcher",
	"Matcher.find":    "",
	"Matcher.group":   "String",
	"Matcher.matches": "",

	// Thread
	"Thread.currentThread": "Thread",
	"Thread.getName":       "String",
	"Thread.sleep":         "",
	"Thread.start":         "",
	"Thread.join":          "",

	// UUID
	"UUID.randomUUID": "UUID",
	"UUID.toString":   "String",

	// Arrays / Collections
	"Arrays.asList": "List", "Arrays.stream": "Stream",
	"Collections.unmodifiableList": "List", "Collections.singletonList": "List",
	"Collections.emptyList": "List",
}

// lookupJDKMethodReturn returns the return type for a JDK method.
// Second return value indicates whether the method was found in the table.
func lookupJDKMethodReturn(className, methodName string) (string, bool) {
	ret, ok := jdkMethodReturns[className+"."+methodName]
	return ret, ok
}

// jdkTypeHierarchy maps className → direct parent types for common JDK classes.
// Used by isJDKSubtype to check type compatibility for overload disambiguation.
var jdkTypeHierarchy = map[string][]string{
	// Throwable hierarchy
	"Throwable":                        {"Object"},
	"Exception":                        {"Throwable"},
	"RuntimeException":                 {"Exception"},
	"Error":                            {"Throwable"},
	"IOException":                      {"Exception"},
	"SQLException":                     {"Exception"},
	"NullPointerException":             {"RuntimeException"},
	"IllegalArgumentException":         {"RuntimeException"},
	"IllegalStateException":            {"RuntimeException"},
	"ClassCastException":               {"RuntimeException"},
	"IndexOutOfBoundsException":        {"RuntimeException"},
	"ArrayIndexOutOfBoundsException":   {"IndexOutOfBoundsException"},
	"StringIndexOutOfBoundsException":  {"IndexOutOfBoundsException"},
	"UnsupportedOperationException":    {"RuntimeException"},
	"ConcurrentModificationException":  {"RuntimeException"},
	"NumberFormatException":            {"IllegalArgumentException"},
	"ArithmeticException":              {"RuntimeException"},
	"SecurityException":                {"RuntimeException"},
	"FileNotFoundException":            {"IOException"},
	"InterruptedException":             {"Exception"},
	"TimeoutException":                 {"Exception"},
	"ExecutionException":               {"Exception"},
	"ClassNotFoundException":           {"Exception"},
	"NoSuchMethodException":            {"Exception"},
	"NoSuchFieldException":             {"Exception"},
	"CloneNotSupportedException":       {"Exception"},
	"ParseException":                   {"Exception"},
	"MalformedURLException":            {"IOException"},
	"SocketException":                  {"IOException"},
	"ConnectException":                 {"SocketException"},
	"SocketTimeoutException":           {"IOException"},
	"EOFException":                     {"IOException"},
	"OutOfMemoryError":                 {"Error"},
	"StackOverflowError":               {"Error"},
	"AssertionError":                   {"Error"},

	// Number hierarchy
	"Number":     {"Object"},
	"Integer":    {"Number"},
	"Long":       {"Number"},
	"Double":     {"Number"},
	"Float":      {"Number"},
	"Short":      {"Number"},
	"Byte":       {"Number"},
	"BigDecimal": {"Number"},
	"BigInteger": {"Number"},

	// String / CharSequence
	"String":        {"CharSequence", "Comparable", "Object"},
	"StringBuilder": {"CharSequence", "Object"},
	"StringBuffer":  {"CharSequence", "Object"},
	"CharSequence":  {"Object"},

	// Collection hierarchy
	"Iterable":   {"Object"},
	"Collection": {"Iterable"},
	"List":       {"Collection"},
	"Set":        {"Collection"},
	"SortedSet":  {"Set"},
	"NavigableSet": {"SortedSet"},
	"Queue":      {"Collection"},
	"Deque":      {"Queue"},
	"ArrayList":  {"List"},
	"LinkedList": {"List", "Deque"},
	"HashSet":    {"Set"},
	"TreeSet":    {"NavigableSet"},
	"LinkedHashSet": {"HashSet"},
	"Vector":     {"List"},
	"Stack":      {"Vector"},
	"PriorityQueue": {"Queue"},
	"ArrayDeque": {"Deque"},
	"CopyOnWriteArrayList": {"List"},

	// Map hierarchy
	"Map":               {"Object"},
	"SortedMap":         {"Map"},
	"NavigableMap":      {"SortedMap"},
	"HashMap":           {"Map"},
	"LinkedHashMap":     {"HashMap"},
	"TreeMap":           {"NavigableMap"},
	"ConcurrentHashMap": {"Map"},
	"Hashtable":         {"Map"},
	"WeakHashMap":       {"Map"},
	"EnumMap":           {"Map"},

	// Boolean / Character / Void
	"Boolean":   {"Comparable", "Object"},
	"Character": {"Comparable", "Object"},
	"Void":      {"Object"},
	"Comparable": {"Object"},

	// Stream / Optional
	"Stream":   {"Object"},
	"Optional": {"Object"},

	// Date / Time
	"Date":          {"Comparable", "Object"},
	"Calendar":      {"Comparable", "Object"},
	"Instant":       {"Comparable", "Object"},
	"LocalDate":     {"Comparable", "Object"},
	"LocalDateTime": {"Comparable", "Object"},
	"LocalTime":     {"Comparable", "Object"},
	"ZonedDateTime": {"Object"},
	"Duration":      {"Comparable", "Object"},

	// IO
	"InputStream":       {"Object"},
	"OutputStream":      {"Object"},
	"Reader":            {"Object"},
	"Writer":            {"Object"},
	"FileInputStream":   {"InputStream"},
	"FileOutputStream":  {"OutputStream"},
	"BufferedReader":     {"Reader"},
	"BufferedWriter":     {"Writer"},
	"InputStreamReader":  {"Reader"},
	"OutputStreamWriter": {"Writer"},
	"PrintWriter":        {"Writer"},
	"PrintStream":        {"OutputStream"},
	"ByteArrayInputStream":  {"InputStream"},
	"ByteArrayOutputStream": {"OutputStream"},
	"StringReader":       {"Reader"},
	"StringWriter":       {"Writer"},
	"File":               {"Comparable", "Object"},
	"Path":               {"Comparable", "Object"},

	// Thread / Runnable
	"Thread":   {"Runnable", "Object"},
	"Runnable": {"Object"},
	"Callable": {"Object"},

	// Enum
	"Enum": {"Comparable", "Object"},

	// Class / Type
	"Class": {"Object"},
	"Type":  {"Object"},

	// Pattern / Matcher
	"Pattern": {"Object"},
	"Matcher": {"Object"},

	// UUID
	"UUID": {"Comparable", "Object"},
}

// isJDKSubtype checks if child is a subtype of parent using the JDK hierarchy table.
func isJDKSubtype(child, parent string) bool {
	if child == parent {
		return true
	}
	ancestors, ok := jdkTypeHierarchy[child]
	if !ok {
		return false
	}
	for _, a := range ancestors {
		if a == parent {
			return true
		}
		if isJDKSubtype(a, parent) {
			return true
		}
	}
	return false
}

// jdkTypeDepth returns the inheritance depth from a type to a target parent.
// Returns -1 if not a subtype. Used to select the most specific overload.
func jdkTypeDepth(child, parent string) int {
	if child == parent {
		return 0
	}
	ancestors, ok := jdkTypeHierarchy[child]
	if !ok {
		return -1
	}
	best := -1
	for _, a := range ancestors {
		if a == parent {
			return 1
		}
		d := jdkTypeDepth(a, parent)
		if d >= 0 && (best < 0 || d+1 < best) {
			best = d + 1
		}
	}
	return best
}
