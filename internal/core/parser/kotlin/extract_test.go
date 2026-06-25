package kotlin

import (
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/core/scanner"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_kotlin "github.com/tree-sitter-grammars/tree-sitter-kotlin/bindings/go"
)

func parseKotlin(code string) *model.ParseResult {
	parser := tree_sitter.NewParser()
	defer parser.Close()
	language := tree_sitter.NewLanguage(tree_sitter_kotlin.Language())
	parser.SetLanguage(language)
	tree := parser.Parse([]byte(code), nil)
	defer tree.Close()

	result := &model.ParseResult{}
	file := scanner.ScannedFile{RelPath: "src/main/kotlin/com/example/Test.kt", Language: "kotlin"}
	Extract(tree.RootNode(), []byte(code), file, result)
	return result
}

func findSymbol(result *model.ParseResult, name string) *model.Symbol {
	for i := range result.Symbols {
		if result.Symbols[i].Name == name {
			return &result.Symbols[i]
		}
	}
	return nil
}

func findSymbolByQN(result *model.ParseResult, qualifiedName string) *model.Symbol {
	for i := range result.Symbols {
		if result.Symbols[i].QualifiedName == qualifiedName {
			return &result.Symbols[i]
		}
	}
	return nil
}

func findCall(result *model.ParseResult, calledName string) *model.RawCall {
	for i := range result.Calls {
		if result.Calls[i].CalledName == calledName {
			return &result.Calls[i]
		}
	}
	return nil
}

func findCallWithReceiver(result *model.ParseResult, receiver, calledName string) *model.RawCall {
	for i := range result.Calls {
		if result.Calls[i].CalledName == calledName && result.Calls[i].ReceiverExpr == receiver {
			return &result.Calls[i]
		}
	}
	return nil
}

func TestExtractClass(t *testing.T) {
	code := `package com.example

class UserService(private val repo: UserRepository) {
    fun findById(id: Long): User? = repo.getById(id)
}

data class User(val id: Long, val name: String)

sealed class Result<out T> {
    data class Success<out T>(val data: T) : Result<T>()
    data class Error(val message: String) : Result<Nothing>()
    object Loading : Result<Nothing>()
}

abstract class BaseRepository<T> {
    abstract suspend fun getById(id: Long): T?
}

@JvmInline
value class UserId(val value: Long)
`
	result := parseKotlin(code)

	tests := []struct {
		name      string
		kind      string
		classType string
	}{
		{"UserService", "Class", "class"},
		{"User", "Class", "data"},
		{"Result", "Class", "sealed"},
		{"Success", "Class", "data"},
		{"Error", "Class", "data"},
		{"Loading", "Class", "object"},
		{"BaseRepository", "Class", "abstract"},
		{"UserId", "Class", "value"},
	}

	for _, tt := range tests {
		symbol := findSymbol(result, tt.name)
		if symbol == nil {
			t.Errorf("symbol %q not found", tt.name)
			continue
		}
		if symbol.Kind != tt.kind {
			t.Errorf("symbol %q: kind = %q, want %q", tt.name, symbol.Kind, tt.kind)
		}
		if symbol.ClassType != tt.classType {
			t.Errorf("symbol %q: classType = %q, want %q", tt.name, symbol.ClassType, tt.classType)
		}
	}
}

func TestExtractInterface(t *testing.T) {
	code := `package com.example

interface Repository<T> {
    suspend fun getAll(): List<T>
    suspend fun getById(id: Long): T?
    fun validate(item: T): Boolean = true
}

sealed interface UiState {
    object Loading : UiState
    data class Success(val items: List<String>) : UiState
}
`
	result := parseKotlin(code)

	repoSymbol := findSymbol(result, "Repository")
	if repoSymbol == nil {
		t.Fatal("Repository not found")
	}
	if repoSymbol.Kind != "Interface" {
		t.Errorf("Repository kind = %q, want Interface", repoSymbol.Kind)
	}

	uiStateSymbol := findSymbol(result, "UiState")
	if uiStateSymbol == nil {
		t.Fatal("UiState not found")
	}
	if uiStateSymbol.Kind != "Interface" {
		t.Errorf("UiState kind = %q, want Interface", uiStateSymbol.Kind)
	}
	if uiStateSymbol.ClassType != "sealed" {
		t.Errorf("UiState classType = %q, want sealed", uiStateSymbol.ClassType)
	}

	loadingSymbol := findSymbol(result, "Loading")
	if loadingSymbol == nil {
		t.Fatal("Loading not found")
	}
	if loadingSymbol.ClassType != "object" {
		t.Errorf("Loading classType = %q, want object", loadingSymbol.ClassType)
	}
}

func TestExtractObject(t *testing.T) {
	code := `package com.example

object AppConfig {
    val baseUrl: String = "https://api.example.com"
    fun getTimeout(): Long = 30_000L
}

class UserRepository {
    companion object {
        private const val TAG = "UserRepository"
        fun create(): UserRepository = UserRepository()
    }
    fun findById(id: Long): User? = null
}

class Factory {
    companion object Creator {
        fun newInstance(): Factory = Factory()
    }
}
`
	result := parseKotlin(code)

	appConfig := findSymbol(result, "AppConfig")
	if appConfig == nil || appConfig.ClassType != "object" {
		t.Errorf("AppConfig: got %v", appConfig)
	}

	companion := findSymbolByQN(result, "com.example.UserRepository.Companion")
	if companion == nil {
		t.Error("UserRepository.Companion not found")
	}

	creator := findSymbolByQN(result, "com.example.Factory.Creator")
	if creator == nil {
		t.Error("Factory.Creator not found")
	}

	createFn := findSymbolByQN(result, "com.example.UserRepository.Companion.create")
	if createFn == nil {
		t.Error("UserRepository.Companion.create not found")
	}

	newInstanceFn := findSymbolByQN(result, "com.example.Factory.Creator.newInstance")
	if newInstanceFn == nil {
		t.Error("Factory.Creator.newInstance not found")
	}
}

func TestExtractFunction(t *testing.T) {
	code := `package com.example

fun calculateTotal(items: List<Item>): Int {
    return items.sum()
}

class OrderService {
    fun createOrder(request: OrderRequest): Order {
        return repository.save(Order(request))
    }

    suspend fun fetchRemote(id: Long): Order {
        return api.getOrder(id)
    }
}
`
	result := parseKotlin(code)

	// Top-level function
	calcFn := findSymbolByQN(result, "com.example.calculateTotal")
	if calcFn == nil {
		t.Error("com.example.calculateTotal not found")
	} else if calcFn.Kind != "Function" {
		t.Errorf("calculateTotal kind = %q, want Function", calcFn.Kind)
	}

	// Member function
	createFn := findSymbolByQN(result, "com.example.OrderService.createOrder")
	if createFn == nil {
		t.Error("com.example.OrderService.createOrder not found")
	}

	// Suspend function
	fetchFn := findSymbolByQN(result, "com.example.OrderService.fetchRemote")
	if fetchFn == nil {
		t.Error("com.example.OrderService.fetchRemote not found")
	} else if !fetchFn.IsAsync {
		t.Error("fetchRemote should be marked async (suspend)")
	}
}

func TestExtractTopLevelFunctionWithClass(t *testing.T) {
	code := `package com.example

fun topLevel(): Int = 42

class MyClass {
    fun memberMethod() {
        topLevel()
    }
}

fun anotherTopLevel() {
    MyClass().memberMethod()
}
`
	result := parseKotlin(code)

	topLevel := findSymbolByQN(result, "com.example.topLevel")
	if topLevel == nil {
		t.Error("com.example.topLevel not found")
	}

	member := findSymbolByQN(result, "com.example.MyClass.memberMethod")
	if member == nil {
		t.Error("com.example.MyClass.memberMethod not found")
	}

	another := findSymbolByQN(result, "com.example.anotherTopLevel")
	if another == nil {
		t.Error("com.example.anotherTopLevel not found")
	}

	// Verify topLevel is NOT under MyClass
	wrongQN := findSymbolByQN(result, "com.example.MyClass.topLevel")
	if wrongQN != nil {
		t.Error("topLevel should NOT be under MyClass")
	}
}

func TestExtractConstructor(t *testing.T) {
	code := `package com.example

class Person(val name: String, var age: Int = 0) {
    var email: String = ""

    constructor(name: String, age: Int, email: String) : this(name, age) {
        this.email = email
    }
}
`
	result := parseKotlin(code)

	nameField := findSymbolByQN(result, "com.example.Person.name")
	if nameField == nil {
		t.Error("Person.name field not found")
	}

	ageField := findSymbolByQN(result, "com.example.Person.age")
	if ageField == nil {
		t.Error("Person.age field not found")
	}

	emailField := findSymbolByQN(result, "com.example.Person.email")
	if emailField == nil {
		t.Error("Person.email field not found")
	}

	// Check init symbols exist
	initCount := 0
	for _, symbol := range result.Symbols {
		if symbol.Name == "<init>" && symbol.IsConstructor {
			initCount++
		}
	}
	if initCount < 2 {
		t.Errorf("expected at least 2 constructors, got %d", initCount)
	}
}

func TestExtractProperty(t *testing.T) {
	code := `package com.example

class Config(val id: Long, private var name: String) {
    val explicit: String = "default"
    var inferred = computeValue()
    val delegated: DB by lazy { createDB() }
    lateinit var binding: ViewBinding
}
`
	result := parseKotlin(code)

	fields := []string{"id", "name", "explicit", "inferred", "delegated", "binding"}
	for _, fieldName := range fields {
		symbol := findSymbolByQN(result, "com.example.Config."+fieldName)
		if symbol == nil {
			t.Errorf("field %q not found", fieldName)
		}
	}

	// Check type hints
	typeHintMap := make(map[string]string)
	for _, hint := range result.TypeHints {
		typeHintMap[hint.VarName] = hint.TypeName
	}
	if typeHintMap["id"] != "Long" {
		t.Errorf("id type = %q, want Long", typeHintMap["id"])
	}
	if typeHintMap["explicit"] != "String" {
		t.Errorf("explicit type = %q, want String", typeHintMap["explicit"])
	}
	if typeHintMap["delegated"] != "DB" {
		t.Errorf("delegated type = %q, want DB", typeHintMap["delegated"])
	}
	if typeHintMap["binding"] != "ViewBinding" {
		t.Errorf("binding type = %q, want ViewBinding", typeHintMap["binding"])
	}
}

func TestExtractCalls(t *testing.T) {
	code := `package com.example

class Service(private val repo: Repository) {
    fun process(id: Long) {
        val user = repo.getUser(id)
        save(user)
        Utils.format(user.name)
    }
}
`
	result := parseKotlin(code)

	getUser := findCallWithReceiver(result, "repo", "getUser")
	if getUser == nil {
		t.Error("call repo.getUser not found")
	}

	save := findCallWithReceiver(result, "", "save")
	if save == nil {
		t.Error("call save() not found")
	}

	format := findCallWithReceiver(result, "Utils", "format")
	if format == nil {
		t.Error("call Utils.format not found")
	}
}

func TestExtractSuperCall(t *testing.T) {
	code := `package com.example

class Child : Parent() {
    override fun method() {
        super.method()
    }
}
`
	result := parseKotlin(code)

	superCall := findCallWithReceiver(result, "super", "method")
	if superCall == nil {
		t.Error("super.method() call not found")
	}
}

func TestExtractLambda(t *testing.T) {
	code := `package com.example

class ViewModel {
    fun loadData() {
        viewModelScope.launch {
            val data = repo.getData()
            process(data)
        }
    }
}
`
	result := parseKotlin(code)

	// Check lambda symbol exists
	var lambdaSymbol *model.Symbol
	for i := range result.Symbols {
		if result.Symbols[i].IsLambda {
			lambdaSymbol = &result.Symbols[i]
			break
		}
	}
	if lambdaSymbol == nil {
		t.Fatal("lambda symbol not found")
	}
	if lambdaSymbol.LambdaContext != "com.example.ViewModel.loadData" {
		t.Errorf("lambda context = %q, want com.example.ViewModel.loadData", lambdaSymbol.LambdaContext)
	}

	// Check PreResolved call to lambda
	var preResolvedCall *model.RawCall
	for i := range result.Calls {
		if result.Calls[i].IsPreResolved {
			preResolvedCall = &result.Calls[i]
			break
		}
	}
	if preResolvedCall == nil {
		t.Fatal("PreResolved call not found")
	}
	if preResolvedCall.LambdaOwnerMethod != "launch" {
		t.Errorf("LambdaOwnerMethod = %q, want launch", preResolvedCall.LambdaOwnerMethod)
	}
	if preResolvedCall.LambdaOwnerReceiver != "viewModelScope" {
		t.Errorf("LambdaOwnerReceiver = %q, want viewModelScope", preResolvedCall.LambdaOwnerReceiver)
	}

	// Check calls inside lambda body
	getData := findCallWithReceiver(result, "repo", "getData")
	if getData == nil {
		t.Error("repo.getData() inside lambda not found")
	}
}

func TestExtractHeritage(t *testing.T) {
	code := `package com.example

class UserRepositoryImpl(private val api: ApiService) : BaseRepository<User>(), UserRepository, Closeable {
    override suspend fun getUser(id: Long): User = api.getUser(id)
    override fun close() {}
}

interface CrudRepository<T> : ReadRepository<T>, WriteRepository<T>
`
	result := parseKotlin(code)

	heritageMap := make(map[string][]string)
	for _, heritage := range result.Heritage {
		heritageMap[heritage.Kind] = append(heritageMap[heritage.Kind], heritage.ParentName)
	}

	// UserRepositoryImpl extends BaseRepository
	extendsFound := false
	for _, parent := range heritageMap["EXTENDS"] {
		if parent == "BaseRepository" {
			extendsFound = true
		}
	}
	if !extendsFound {
		t.Error("EXTENDS BaseRepository not found")
	}

	// UserRepositoryImpl implements UserRepository, Closeable
	implementsCount := 0
	for _, parent := range heritageMap["IMPLEMENTS"] {
		if parent == "UserRepository" || parent == "Closeable" || parent == "ReadRepository" || parent == "WriteRepository" {
			implementsCount++
		}
	}
	if implementsCount < 2 {
		t.Errorf("expected at least 2 IMPLEMENTS, got %d", implementsCount)
	}
}

func TestExtractAnnotations(t *testing.T) {
	code := `package com.example

@HiltViewModel
class OrderViewModel @Inject constructor(
    private val repository: OrderRepository
) : ViewModel()

@Entity(tableName = "users")
data class UserEntity(
    @PrimaryKey val id: Long,
    @ColumnInfo(name = "user_name") val name: String
)
`
	result := parseKotlin(code)

	viewModel := findSymbol(result, "OrderViewModel")
	if viewModel == nil {
		t.Fatal("OrderViewModel not found")
	}
	hiltFound := false
	for _, annotation := range viewModel.Annotations {
		if annotation.Name == "HiltViewModel" {
			hiltFound = true
		}
	}
	if !hiltFound {
		t.Error("@HiltViewModel annotation not found on OrderViewModel")
	}

	entity := findSymbol(result, "UserEntity")
	if entity == nil {
		t.Fatal("UserEntity not found")
	}
	entityFound := false
	for _, annotation := range entity.Annotations {
		if annotation.Name == "Entity" {
			entityFound = true
			if annotation.Params["value"] == "" {
				t.Error("@Entity params should contain tableName")
			}
		}
	}
	if !entityFound {
		t.Error("@Entity annotation not found on UserEntity")
	}
}

func TestExtractImports(t *testing.T) {
	code := `package com.example.app

import android.os.Bundle
import com.example.service.UserService
`
	result := parseKotlin(code)

	if len(result.Imports) < 2 {
		t.Fatalf("expected at least 2 imports, got %d", len(result.Imports))
	}

	importMap := make(map[string]string)
	for _, imp := range result.Imports {
		importMap[imp.SymbolName] = imp.ModulePath
	}
	if importMap["Bundle"] != "android.os.Bundle" {
		t.Errorf("Bundle import path = %q", importMap["Bundle"])
	}
	if importMap["UserService"] != "com.example.service.UserService" {
		t.Errorf("UserService import path = %q", importMap["UserService"])
	}
}

func TestExtractNestedClassQN(t *testing.T) {
	code := `package com.example

class Outer {
    class Middle {
        class Deep {
            fun method() {}
        }
    }
}
`
	result := parseKotlin(code)

	tests := []string{
		"com.example.Outer",
		"com.example.Outer.Middle",
		"com.example.Outer.Middle.Deep",
		"com.example.Outer.Middle.Deep.method",
	}
	for _, expectedQN := range tests {
		if findSymbolByQN(result, expectedQN) == nil {
			t.Errorf("QN %q not found", expectedQN)
		}
	}
}

func TestExtractEnumEntries(t *testing.T) {
	code := `package com.example

enum class Status(val code: Int) {
    ACTIVE(1),
    INACTIVE(0);
    fun isActive(): Boolean = this == ACTIVE
}
`
	result := parseKotlin(code)

	active := findSymbolByQN(result, "com.example.Status.ACTIVE")
	if active == nil {
		t.Error("Status.ACTIVE not found")
	} else if active.Kind != "Variable" {
		t.Errorf("ACTIVE kind = %q, want Variable", active.Kind)
	}

	inactive := findSymbolByQN(result, "com.example.Status.INACTIVE")
	if inactive == nil {
		t.Error("Status.INACTIVE not found")
	}

	isActiveFn := findSymbolByQN(result, "com.example.Status.isActive")
	if isActiveFn == nil {
		t.Error("Status.isActive not found")
	}
}

func TestExtractSafeCallChain(t *testing.T) {
	code := `package com.example

fun process(user: User?) {
    user?.name?.trim()
    user?.let { sendEmail(it.email) }
}
`
	result := parseKotlin(code)

	// user?.name?.trim() — trim is a call, name is property access (not a call)
	trimCall := findCall(result, "trim")
	if trimCall == nil {
		t.Error("trim() call not found")
	}

	letCall := findCallWithReceiver(result, "user", "let")
	if letCall == nil {
		t.Error("user?.let call not found")
	}

	// sendEmail inside lambda
	sendEmail := findCall(result, "sendEmail")
	if sendEmail == nil {
		t.Error("sendEmail inside let lambda not found")
	}
}

func TestExtractChainCall(t *testing.T) {
	code := `package com.example

fun process() {
    repo.getUser(id).also { cache.put(it) }.let { mapToDto(it) }
}
`
	result := parseKotlin(code)

	getUser := findCallWithReceiver(result, "repo", "getUser")
	if getUser == nil {
		t.Error("repo.getUser not found")
	}

	also := findCall(result, "also")
	if also == nil {
		t.Error(".also call not found")
	}

	let := findCall(result, "let")
	if let == nil {
		t.Error(".let call not found")
	}

	put := findCallWithReceiver(result, "cache", "put")
	if put == nil {
		t.Error("cache.put inside also lambda not found")
	}

	mapToDto := findCall(result, "mapToDto")
	if mapToDto == nil {
		t.Error("mapToDto inside let lambda not found")
	}
}

func TestExtractLambdaWithArgs(t *testing.T) {
	code := `package com.example

fun example() {
    withContext(Dispatchers.IO) {
        doWork()
    }
}
`
	result := parseKotlin(code)

	withContext := findCall(result, "withContext")
	if withContext == nil {
		t.Fatal("withContext call not found")
	}
	if withContext.ArgCount != 1 {
		t.Errorf("withContext ArgCount = %d, want 1", withContext.ArgCount)
	}

	doWork := findCall(result, "doWork")
	if doWork == nil {
		t.Error("doWork inside lambda not found")
	}
}

func TestExtractDelegateCalls(t *testing.T) {
	code := `package com.example

class Repo {
    val db: AppDatabase by lazy { AppDatabase.create(context) }
}
`
	result := parseKotlin(code)

	create := findCallWithReceiver(result, "AppDatabase", "create")
	if create == nil {
		t.Error("AppDatabase.create inside lazy delegate not found")
	}
}

func TestExtractExtensionFunction(t *testing.T) {
	code := `package com.example

fun String.toSlug(): String {
    return this.lowercase().replace(" ", "-")
}

fun calculateTotal(): Int = 42
`
	result := parseKotlin(code)

	toSlug := findSymbolByQN(result, "com.example.toSlug")
	if toSlug == nil {
		t.Fatal("com.example.toSlug not found")
	}
	if toSlug.Kind != "Function" {
		t.Errorf("toSlug kind = %q, want Function", toSlug.Kind)
	}

	// Verify it's not confused with regular top-level function
	calcTotal := findSymbolByQN(result, "com.example.calculateTotal")
	if calcTotal == nil {
		t.Error("com.example.calculateTotal not found")
	}
}

func TestExtractCustomGetter(t *testing.T) {
	code := `package com.example

class User(val birthYear: Int) {
    val age: Int
        get() = Calendar.getInstance().get(Calendar.YEAR) - birthYear
}
`
	result := parseKotlin(code)

	getInstance := findCallWithReceiver(result, "Calendar", "getInstance")
	if getInstance == nil {
		t.Error("Calendar.getInstance() inside custom getter not found")
	}
}

func TestExtractScopeFunctions(t *testing.T) {
	code := `package com.example

fun configure() {
    OkHttpClient.Builder().apply {
        connectTimeout(30)
        addInterceptor(loggingInterceptor)
    }.build()
}
`
	result := parseKotlin(code)

	apply := findCall(result, "apply")
	if apply == nil {
		t.Error(".apply call not found")
	}

	connectTimeout := findCall(result, "connectTimeout")
	if connectTimeout == nil {
		t.Error("connectTimeout inside apply lambda not found")
	}

	addInterceptor := findCall(result, "addInterceptor")
	if addInterceptor == nil {
		t.Error("addInterceptor inside apply lambda not found")
	}

	build := findCall(result, "build")
	if build == nil {
		t.Error(".build() call not found")
	}
}

func TestExtractConstructorCall(t *testing.T) {
	code := `package com.example

fun create() {
    val user = User("name", 30)
    val list = listOf(1, 2, 3)
}
`
	result := parseKotlin(code)

	userCall := findCall(result, "User")
	if userCall == nil {
		t.Fatal("User() constructor call not found")
	}
	if userCall.ArgCount != 2 {
		t.Errorf("User ArgCount = %d, want 2", userCall.ArgCount)
	}

	listOfCall := findCall(result, "listOf")
	if listOfCall == nil {
		t.Fatal("listOf() call not found")
	}
	if listOfCall.ArgCount != 3 {
		t.Errorf("listOf ArgCount = %d, want 3", listOfCall.ArgCount)
	}
}
